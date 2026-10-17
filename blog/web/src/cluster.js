// A simulated Kubernetes cluster running this blog, powered by webernetes.
// Everything here runs in the visitor's browser; nothing talks to a backend.
import { BaseImage, Cluster } from "@ngrok/webernetes";

export const NAMESPACE = "blog";
export const DEPLOYMENT = "blog";
export const SERVICE_URL = "http://node-1:30080";

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
		ctx.writeStdout(`Server listening on :8080\n`);
		ctx.setTimeout(() => {
			ready = true;
			ctx.writeStdout(`Posts loaded, ready to serve traffic\n`);
		}, 1500);

		ctx.listenHttp(8080, async (_requestCtx, request) => {
			switch (request.url.pathname) {
				case "/healthz":
					return { status: 200, body: "ok\n" };
				case "/readyz":
					return ready ? { status: 200, body: "ok\n" } : { status: 503, body: "not ready\n" };
				default:
					return { status: 200, body: `Hello from ${ctx.pod.name}\n` };
			}
		});

		return await ctx.waitUntilKilled();
	}

	async signalHandler(ctx, signal) {
		if (signal === "SIGTERM") {
			ctx.writeStdout("Shutting down\n");
			await ctx.sleep(1500);
			ctx.exit(0);
		}
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
		cluster.registerImage(BlogImage);
		await cluster.init();
		await cluster.apply(manifests(replicas));
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
	const phase = pod.status?.phase;
	if (!pod.spec?.nodeName || !phase) return "Pending";
	if (phase === "Pending") return "ContainerCreating";
	if (phase === "Running") {
		const ready = pod.status.conditions?.some((c) => c.type === "Ready" && c.status === "True");
		return ready ? "Running" : "Starting";
	}
	return phase;
}
