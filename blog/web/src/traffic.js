// Load balancing: send HTTP requests to the blog Service and watch them get
// spread across the pods. Combined with the Chaos Monkey it shows how
// readiness probes keep traffic away from pods that are going away.
import { bootCluster, NAMESPACE, podStatus, SERVICE_URL } from "./cluster.js";

const RATE_PER_SECOND = 8;
const MAX_LOG = 6;
const DOT_MS = 450;
const TIMEOUT_MS = 5000;

const ROW_STYLES = {
	Running: "border-emerald-500/30 text-emerald-300",
	Terminating: "border-rose-500/30 text-rose-300 opacity-60",
};
const DEFAULT_ROW_STYLE = "border-cluster-600 text-slate-400";
const STATUS_LABELS = { Running: "ready", ContainerCreating: "creating" };

function el(tag, className = "", text = "") {
	const node = document.createElement(tag);
	if (className) node.className = className;
	if (text) node.textContent = text;
	return node;
}

const shortName = (name) => `blog-…-${name.slice(name.lastIndexOf("-") + 1)}`;

export async function mountTraffic(root) {
	const body = root.querySelector("[data-traffic-body]");
	const summary = root.querySelector("[data-traffic-summary]");
	const startButton = root.querySelector("[data-traffic-start]");
	startButton.disabled = true;
	startButton.textContent = "Booting cluster…";

	let cluster;
	try {
		cluster = await bootCluster();
	} catch (err) {
		console.error(err);
		startButton.textContent = "Boot failed – 503 Service Unavailable 😵";
		return;
	}

	const reducedMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
	const pods = new Map();
	const hits = new Map();
	const log = [];
	let total = 0;
	let failed = 0;
	let timer = null;

	body.replaceChildren(...body.querySelectorAll("h2"));
	const hint = el("p", "mb-5 text-sm text-slate-400", "Every request goes to the Service, which picks a ready pod round-robin. Start the traffic, then kill pods in the Chaos Monkey above: requests keep succeeding because terminating pods are taken out of rotation first.");

	const controls = el("div", "mb-5 flex flex-wrap items-center gap-2 font-mono text-sm");
	const btn = "rounded-md border border-cluster-600 bg-cluster-800 px-3 py-1 text-slate-200 transition hover:border-k8s hover:text-white";
	const once = el("button", btn, "curl blog");
	const burst = el("button", btn, "curl ×20");
	const toggle = el("button", "rounded-md bg-k8s px-3 py-1 font-semibold text-white shadow-lg shadow-k8s/30 transition hover:bg-k8s-light");
	const reset = el("button", "ml-auto rounded-md px-2 py-1 text-xs text-slate-500 transition hover:text-slate-300", "reset");
	for (const b of [once, burst, toggle, reset]) b.type = "button";
	controls.append(once, burst, toggle, reset);

	// Service box on the left, pods on the right; dots fly between them.
	const stage = el("div", "relative grid grid-cols-[auto_1fr] items-center gap-4 sm:gap-10");
	const service = el("div", "relative z-10 rounded-lg border border-k8s/50 bg-k8s/10 px-3 py-4 text-center font-mono text-xs text-k8s-light sm:px-5");
	service.append(el("div", "text-lg", "⚖️"), el("div", "font-semibold text-white", "svc/blog"), el("div", "text-slate-500", ":80 → :8080"));
	const podList = el("div", "relative z-10 space-y-1.5");
	const dots = el("div", "pointer-events-none absolute inset-0 z-20");
	stage.append(service, podList, dots);

	const stats = el("div", "mt-5 flex flex-wrap gap-x-5 gap-y-1 font-mono text-xs text-slate-400");
	const logBox = el("ol", "mt-3 space-y-0.5 rounded-lg border border-cluster-700 bg-cluster-950 p-3 font-mono text-[11px] leading-5 sm:text-xs");
	body.append(hint, controls, stage, stats, logBox);

	function renderStats() {
		const rate = total ? (((total - failed) / total) * 100).toFixed(1) : "100.0";
		stats.replaceChildren(
			el("span", "", `requests: ${total}`),
			el("span", failed ? "text-rose-300" : "", `errors: ${failed}`),
			el("span", failed ? "text-amber-300" : "text-emerald-300", `success: ${rate}%`),
		);
		summary.textContent = timer ? `${RATE_PER_SECOND} req/s` : "idle";
		toggle.textContent = timer ? "■ Stop traffic" : `▶ Start traffic (${RATE_PER_SECOND} req/s)`;
	}

	function renderLog() {
		logBox.replaceChildren();
		for (const entry of log) {
			const row = el("li", "flex gap-3 whitespace-nowrap");
			row.append(el("span", "text-slate-600", "GET /"), el("span", entry.ok ? "text-emerald-300" : "text-rose-300", entry.status), el("span", "truncate text-slate-400", entry.target), el("span", "ml-auto text-slate-600", `${entry.ms}ms`));
			logBox.append(row);
		}
		if (!log.length) logBox.append(el("li", "text-slate-600", "$ curl blog  # waiting for requests…"));
	}

	function renderPods() {
		const max = Math.max(1, ...hits.values());
		podList.replaceChildren();
		const sorted = [...pods.values()].sort((a, b) => a.metadata.name.localeCompare(b.metadata.name));
		for (const pod of sorted) {
			const name = pod.metadata.name;
			const status = podStatus(pod);
			const count = hits.get(name) ?? 0;
			const row = el("div", `relative overflow-hidden rounded-md border bg-cluster-950/60 px-2.5 py-1.5 font-mono text-xs transition-colors ${ROW_STYLES[status] ?? DEFAULT_ROW_STYLE}`);
			row.dataset.pod = name;
			const bar = el("div", "absolute inset-y-0 left-0 bg-k8s/15 transition-all duration-300");
			bar.style.width = `${(count / max) * 100}%`;
			const content = el("div", "relative flex items-center gap-2");
			content.append(el("span", "truncate", shortName(name)), el("span", "text-[10px] uppercase tracking-wide text-slate-500", STATUS_LABELS[status] ?? status), el("span", "ml-auto tabular-nums text-slate-300", String(count)));
			row.append(bar, content);
			podList.append(row);
		}
		if (!sorted.length) podList.append(el("p", "font-mono text-xs text-rose-300", "no pods – every request will fail 😱"));
	}

	// fly animates a dot from the Service to the pod that answered, or lets
	// it fizzle out at the Service when the request failed.
	function fly(podName, ok) {
		if (reducedMotion) return;
		const stageBox = stage.getBoundingClientRect();
		const from = service.getBoundingClientRect();
		const dot = el("span", `absolute h-2.5 w-2.5 -ml-1 -mt-1 rounded-full shadow-lg transition-all ease-out ${ok ? "bg-cyan-300 shadow-cyan-400/60" : "bg-rose-400 shadow-rose-500/60"}`);
		dot.style.transitionDuration = `${DOT_MS}ms`;
		dot.style.left = `${from.right - stageBox.left}px`;
		dot.style.top = `${from.top + from.height / 2 - stageBox.top}px`;
		dots.append(dot);
		requestAnimationFrame(() =>
			requestAnimationFrame(() => {
				// Look the row up now: the list may have been re-rendered since.
				const target = ok && podList.querySelector(`[data-pod="${CSS.escape(podName)}"]`);
				if (target) {
					const to = target.getBoundingClientRect();
					dot.style.left = `${to.left - stageBox.left + 12}px`;
					dot.style.top = `${to.top + to.height / 2 - stageBox.top}px`;
				} else {
					dot.style.transform = "scale(3)";
					dot.style.opacity = "0";
				}
			}),
		);
		setTimeout(() => dot.remove(), DOT_MS + 50);
	}

	async function request() {
		const started = performance.now();
		let entry;
		try {
			const response = await Promise.race([
				cluster.fetch(`${SERVICE_URL}/`),
				new Promise((_, reject) => setTimeout(() => reject(new Error("timeout")), TIMEOUT_MS)),
			]);
			const name = String(response.body ?? "").match(/blog-[\w-]+/)?.[0];
			const ok = response.status >= 200 && response.status < 400;
			entry = { ok, status: String(response.status), target: name ?? "" };
			if (ok && name) hits.set(name, (hits.get(name) ?? 0) + 1);
		} catch (err) {
			const reason = err?.message === "timeout" ? "timed out" : "connection refused – no ready pods";
			entry = { ok: false, status: "ERR", target: reason };
		}
		entry.ms = Math.max(1, Math.round(performance.now() - started));
		total++;
		if (!entry.ok) failed++;
		log.unshift(entry);
		log.length = Math.min(log.length, MAX_LOG);
		fly(entry.target, entry.ok);
		setTimeout(renderPods, reducedMotion ? 0 : DOT_MS);
		renderStats();
		renderLog();
	}

	once.addEventListener("click", () => request());
	burst.addEventListener("click", () => {
		for (let i = 0; i < 20; i++) setTimeout(request, i * 60);
	});
	toggle.addEventListener("click", () => {
		if (timer) {
			clearInterval(timer);
			timer = null;
		} else {
			timer = setInterval(request, 1000 / RATE_PER_SECOND);
		}
		renderStats();
	});
	reset.addEventListener("click", () => {
		hits.clear();
		log.length = 0;
		total = failed = 0;
		renderPods();
		renderStats();
		renderLog();
	});

	cluster.informer(
		"pods",
		(type, pod) => {
			if (type === "delete") {
				pods.delete(pod.metadata.name);
				hits.delete(pod.metadata.name);
			} else pods.set(pod.metadata.name, pod);
			renderPods();
		},
		{ namespace: NAMESPACE },
	);

	renderPods();
	renderStats();
	renderLog();
}
