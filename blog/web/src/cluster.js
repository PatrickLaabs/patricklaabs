// A simulated Kubernetes cluster running this blog, powered by webernetes.
// Everything here runs in the visitor's browser; nothing talks to a backend.
import { BaseImage, Cluster } from "@ngrok/webernetes";

export const NAMESPACE = "blog";
export const DEPLOYMENT = "blog";
export const SERVICE_URL = "http://node-1:30080";

// Container stdout, keyed by pod name. webernetes has no log API, so the
// image records what it writes here for `kubectl logs`.
const logs = new Map();
const MAX_LOG_LINES = 200;

function log(ctx, line) {
	ctx.writeStdout(`${line}\n`);
	const lines = logs.get(ctx.pod.name) ?? [];
	lines.push(`${new Date().toISOString()} ${line}`);
	if (lines.length > MAX_LOG_LINES) lines.shift();
	logs.set(ctx.pod.name, lines);
}

export function podLogs(name) {
	return logs.get(name);
}

// BlogImage is the container image the blog's pods run. It answers HTTP on
// port 8080, takes a moment to become ready and drains gracefully on SIGTERM,
// so the pod lifecycle is slow enough to watch.
class BlogImage extends BaseImage {
	static imageName = "patricklaabs/blog";
	static imageVersion = "1.0";
	defaultCommand = ["server"];

	async exec(ctx, argv) {
		if (argv[0] !== "server") {
			return await super.exec(ctx, argv);
		}

		let ready = false;
		log(ctx, "Server listening on :8080");
		ctx.setTimeout(() => {
			ready = true;
			log(ctx, "Posts loaded, ready to serve traffic");
		}, 1500);

		ctx.listenHttp(8080, async (_requestCtx, request) => {
			switch (request.url.pathname) {
				case "/healthz":
					return { status: 200, body: "ok\n" };
				case "/readyz":
					return ready ? { status: 200, body: "ok\n" } : { status: 503, body: "not ready\n" };
				default:
					log(ctx, `GET ${request.url.pathname} 200`);
					return { status: 200, body: `Hello from ${ctx.pod.name}\n` };
			}
		});

		return await ctx.waitUntilKilled();
	}

	async signalHandler(ctx, signal) {
		if (signal === "SIGTERM") {
			log(ctx, "SIGTERM received, draining connections");
			await ctx.sleep(1500);
			ctx.exit(0);
		}
	}
}

// Extra images for the live manifests in posts. Anything else fails with
// ErrImagePull, which is a lesson of its own.
class NginxImage extends BaseImage {
	static imageName = "nginx";
	static imageVersion = "1.27";
	defaultCommand = ["nginx"];

	async exec(ctx, argv) {
		if (argv[0] !== "nginx") {
			return await super.exec(ctx, argv);
		}
		log(ctx, "nginx: ready for start up");
		ctx.listenHttp(80, async (_requestCtx, request) => {
			log(ctx, `"GET ${request.url.pathname} HTTP/1.1" 200`);
			return { status: 200, body: `<h1>Welcome to nginx!</h1>\nServed by ${ctx.pod.name}\n` };
		});
		return await ctx.waitUntilKilled();
	}
}

class BusyboxImage extends BaseImage {
	static imageName = "busybox";
	static imageVersion = "1.36";
	defaultCommand = ["sh"];
}

// CrashLoopImage starts, complains and exits non-zero, forever.
class CrashLoopImage extends BaseImage {
	static imageName = "patricklaabs/crashloop";
	static imageVersion = "1.0";
	defaultCommand = ["run"];

	async exec(ctx, argv) {
		if (argv[0] !== "run") {
			return await super.exec(ctx, argv);
		}
		log(ctx, "Starting up…");
		await ctx.sleep(1000);
		log(ctx, "panic: out of coffee ☕");
		return 1;
	}
}

function manifests(replicas) {
	return [
		{ apiVersion: "v1", kind: "Namespace", metadata: { name: NAMESPACE } },
		{
			apiVersion: "apps/v1",
			kind: "Deployment",
			metadata: { name: DEPLOYMENT, namespace: NAMESPACE, labels: { app: "blog" } },
			spec: {
				replicas,
				selector: { matchLabels: { app: "blog" } },
				template: {
					metadata: { labels: { app: "blog" } },
					spec: {
						terminationGracePeriodSeconds: 5,
						containers: [
							{
								name: "blog",
								image: `${BlogImage.imageName}:${BlogImage.imageVersion}`,
								ports: [{ containerPort: 8080 }],
								livenessProbe: { httpGet: { path: "/healthz", port: 8080 }, periodSeconds: 2 },
								readinessProbe: { httpGet: { path: "/readyz", port: 8080 }, periodSeconds: 1 },
							},
						],
					},
				},
			},
		},
		{
			apiVersion: "v1",
			kind: "Service",
			metadata: { name: "blog", namespace: NAMESPACE },
			spec: {
				type: "NodePort",
				selector: { app: "blog" },
				ports: [{ port: 80, targetPort: 8080, nodePort: 30080 }],
			},
		},
	];
}

let booting;

// bootCluster starts the cluster once and returns the same instance to every
// caller, so all widgets on the page share one cluster.
export function bootCluster({ replicas = 3 } = {}) {
	booting ??= (async () => {
		const cluster = new Cluster();
		for (const image of [BlogImage, NginxImage, BusyboxImage, CrashLoopImage]) {
			cluster.registerImage(image);
		}
		await cluster.init();
		await cluster.apply(manifests(replicas));
		// Give the controllers a moment to create the pods, so the first
		// `kubectl get pods` does not come back empty.
		for (let i = 0; i < 60; i++) {
			const pods = await cluster.api.corev1.listNamespacedPod({ namespace: NAMESPACE });
			if (pods.items.length >= replicas) break;
			await new Promise((resolve) => setTimeout(resolve, 50));
		}
		return cluster;
	})();
	return booting;
}

export async function killPod(cluster, name) {
	await cluster.api.corev1.deleteNamespacedPod({ namespace: NAMESPACE, name });
}

export async function scaleDeployment(cluster, replicas) {
	const deployment = await cluster.api.appsv1.readNamespacedDeployment({ namespace: NAMESPACE, name: DEPLOYMENT });
	deployment.spec.replicas = replicas;
	await cluster.api.appsv1.replaceNamespacedDeployment({ namespace: NAMESPACE, name: DEPLOYMENT, body: deployment });
}

// podStatus mirrors the STATUS column of `kubectl get pods`.
export function podStatus(pod) {
	if (pod.metadata?.deletionTimestamp) return "Terminating";
	const waiting = pod.status?.containerStatuses?.find((s) => s.state?.waiting?.reason && s.state.waiting.reason !== "ContainerCreating");
	if (waiting) return waiting.state.waiting.reason;
	const phase = pod.status?.phase;
	if (!pod.spec?.nodeName || !phase) return "Pending";
	if (phase === "Pending") return "ContainerCreating";
	if (phase === "Running") {
		const ready = pod.status.conditions?.some((c) => c.type === "Ready" && c.status === "True");
		return ready ? "Running" : "Starting";
	}
	return phase;
}

// age formats a timestamp like the AGE column of kubectl.
export function age(timestamp) {
	if (!timestamp) return "<unknown>";
	const seconds = Math.max(0, Math.floor((Date.now() - new Date(timestamp).getTime()) / 1000));
	if (seconds < 120) return `${seconds}s`;
	const minutes = Math.floor(seconds / 60);
	if (minutes < 10) return `${minutes}m${seconds % 60}s`;
	if (minutes < 180) return `${minutes}m`;
	return `${Math.floor(minutes / 60)}h`;
}
