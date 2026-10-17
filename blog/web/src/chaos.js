// Chaos Monkey: kill the blog's pods and watch Kubernetes heal itself.
import { bootCluster, killPod, NAMESPACE, podStatus, scaleDeployment } from "./cluster.js";

const MAX_REPLICAS = 9;
const MONKEY_INTERVAL_MS = 1200;
const MONKEY_DURATION_MS = 15000;
const MAX_EVENTS = 7;

const STATUS_STYLES = {
	Pending: { tile: "border-slate-600 bg-slate-500/5 text-slate-400", dot: "bg-slate-400" },
	ContainerCreating: { tile: "border-amber-500/40 bg-amber-500/5 text-amber-300", dot: "bg-amber-400" },
	Starting: { tile: "border-sky-500/40 bg-sky-500/5 text-sky-300", dot: "bg-sky-400 animate-pulse" },
	Running: {
		tile: "border-emerald-500/40 bg-emerald-500/5 text-emerald-300 hover:border-rose-500/70 hover:bg-rose-500/10 cursor-crosshair",
		dot: "bg-emerald-400",
	},
	Terminating: { tile: "border-rose-500/40 bg-rose-500/5 text-rose-300 opacity-60 line-through", dot: "bg-rose-400" },
};

// Shorter labels than kubectl's STATUS column so they fit on a tile.
const STATUS_LABELS = { ContainerCreating: "Creating" };

const BADGE_STYLES = {
	healthy: "border-emerald-500/30 bg-emerald-500/10 text-emerald-300",
	degraded: "border-amber-500/30 bg-amber-500/10 text-amber-300",
	down: "border-rose-500/30 bg-rose-500/10 text-rose-300",
};
const BADGE_DOT_STYLES = { healthy: "bg-emerald-400", degraded: "bg-amber-400", down: "bg-rose-400" };

const ACHIEVEMENTS = [
	[1, "🩸 First blood! Kubernetes already noticed."],
	[5, "🎖️ Junior Chaos Engineer"],
	[10, "🔥 You are your SRE team's worst nightmare."],
	[25, "🐒 Netflix called, they want to hire you."],
	[50, "☠️ Have you considered a career in production incidents?"],
];

function el(tag, className = "", text = "") {
	const node = document.createElement(tag);
	if (className) node.className = className;
	if (text) node.textContent = text;
	return node;
}

function loadKills() {
	try {
		return Number(localStorage.getItem("chaos-monkey-kills")) || 0;
	} catch {
		return 0;
	}
}

function saveKills(kills) {
	try {
		localStorage.setItem("chaos-monkey-kills", String(kills));
	} catch {
		// Storage can be unavailable (private mode); the counter just resets.
	}
}

export async function mountChaos(root) {
	const body = root.querySelector("[data-chaos-body]");
	const summary = root.querySelector("[data-chaos-summary]");
	const startButton = root.querySelector("[data-chaos-start]");
	startButton.disabled = true;
	startButton.textContent = "Booting cluster…";
	summary.textContent = "cluster: booting";

	let cluster;
	try {
		cluster = await bootCluster();
	} catch (err) {
		console.error(err);
		startButton.textContent = "Boot failed – CrashLoopBackOff 😵";
		summary.textContent = "cluster: failed";
		return;
	}
	const bootedAt = performance.now();

	const pods = new Map();
	const nodes = new Set();
	const events = [];
	let desired = 3;
	let kills = loadKills();
	let healStart = null;
	let monkeyTimer = null;
	let monkeyStop = null;

	// Static layout, filled in by render(). The heading stays, the intro goes.
	body.replaceChildren(...body.querySelectorAll("h2"));
	const toolbar = el("div", "flex flex-wrap items-center justify-between gap-3 mb-5 font-mono text-sm");
	const title = el("div", "flex items-center gap-2");
	const deploymentName = el("span", "text-slate-500", "deployment/");
	deploymentName.append(el("span", "text-white font-semibold", "blog"));
	title.append(deploymentName);
	const readyBadge = el("span", "rounded px-2 py-0.5 text-xs");
	title.append(readyBadge);

	const controls = el("div", "flex flex-wrap items-center gap-2");
	const btn = "rounded-md border border-cluster-600 bg-cluster-800 px-2.5 py-1 text-slate-200 transition hover:border-k8s hover:text-white disabled:opacity-40 disabled:hover:border-cluster-600";
	const minus = el("button", btn, "−");
	const replicasLabel = el("span", "min-w-[6.5rem] text-center text-slate-300");
	const plus = el("button", btn, "+");
	minus.type = plus.type = "button";
	minus.setAttribute("aria-label", "Scale down");
	plus.setAttribute("aria-label", "Scale up");
	const monkey = el("button", "rounded-md bg-rose-600 px-3 py-1 font-semibold text-white shadow-lg shadow-rose-900/40 transition hover:bg-rose-500");
	monkey.type = "button";
	controls.append(minus, replicasLabel, plus, monkey);
	toolbar.append(title, controls);

	const nodeGrid = el("div", "grid grid-cols-1 gap-3 sm:grid-cols-3");
	const stats = el("div", "mt-5 flex flex-wrap items-center justify-between gap-2 font-mono text-xs");
	const killsLabel = el("span", "text-slate-400");
	const healLabel = el("span", "text-emerald-300");
	stats.append(killsLabel, healLabel);
	const achievement = el("p", "mt-2 min-h-[1.25rem] font-mono text-xs text-amber-300");
	const eventsBox = el("div", "mt-4 rounded-lg border border-cluster-700 bg-cluster-950 p-3 font-mono text-[11px] leading-5 sm:text-xs");
	const eventsTitle = el("p", "mb-1 text-slate-500", `$ kubectl get events -n ${NAMESPACE} -w`);
	const eventsList = el("ol", "space-y-0.5");
	eventsBox.append(eventsTitle, eventsList);
	const hint = el("p", "mb-4 text-sm text-slate-400", "Click a running pod to kill it. The ReplicaSet controller will notice and schedule a replacement.");
	body.append(hint, toolbar, nodeGrid, stats, achievement, eventsBox);

	const badge = document.querySelector("[data-cluster-badge]");
	const badgeDot = badge?.querySelector("[data-cluster-badge-dot]");
	const badgeText = badge?.querySelector("[data-cluster-badge-text]");
	const badgeBase = badge?.dataset.base ?? "";

	let scheduled = false;
	const scheduleRender = () => {
		if (scheduled) return;
		scheduled = true;
		requestAnimationFrame(() => {
			scheduled = false;
			render();
		});
	};

	function readyCount() {
		let ready = 0;
		for (const pod of pods.values()) if (podStatus(pod) === "Running") ready++;
		return ready;
	}

	function kill(name) {
		const pod = pods.get(name);
		if (!pod || podStatus(pod) === "Terminating") return;
		kills++;
		saveKills(kills);
		const hit = ACHIEVEMENTS.find(([n]) => n === kills);
		if (hit) achievement.textContent = hit[1];
		killPod(cluster, name).catch((err) => console.error(err));
		scheduleRender();
	}

	function stopMonkey() {
		clearInterval(monkeyTimer);
		clearTimeout(monkeyStop);
		monkeyTimer = monkeyStop = null;
		scheduleRender();
	}

	function startMonkey() {
		const strike = () => {
			const victims = [...pods.values()].filter((p) => podStatus(p) !== "Terminating");
			if (victims.length) kill(victims[Math.floor(Math.random() * victims.length)].metadata.name);
		};
		strike();
		monkeyTimer = setInterval(strike, MONKEY_INTERVAL_MS);
		monkeyStop = setTimeout(stopMonkey, MONKEY_DURATION_MS);
		scheduleRender();
	}

	monkey.addEventListener("click", () => (monkeyTimer ? stopMonkey() : startMonkey()));

	async function scale(delta) {
		const next = Math.min(MAX_REPLICAS, Math.max(0, desired + delta));
		if (next === desired) return;
		desired = next;
		scheduleRender();
		await scaleDeployment(cluster, next).catch((err) => console.error(err));
	}
	// Tiles are re-rendered on every pod update, so a click (pointer down and
	// up on the same element) can get lost. Kill on pointerdown instead, and
	// keep click for keyboard activation.
	const tileTarget = (event) => event.target.closest("[data-pod]");
	nodeGrid.addEventListener("pointerdown", (event) => {
		const tile = tileTarget(event);
		if (tile && event.button === 0) kill(tile.dataset.pod);
	});
	nodeGrid.addEventListener("click", (event) => {
		const tile = tileTarget(event);
		if (tile && event.detail === 0) kill(tile.dataset.pod);
	});
	minus.addEventListener("click", () => scale(-1));
	plus.addEventListener("click", () => scale(1));

	function podTile(pod) {
		const status = podStatus(pod);
		const style = STATUS_STYLES[status] ?? STATUS_STYLES.Pending;
		const name = pod.metadata.name;
		const tile = el("button", `flex w-full items-center gap-2 rounded-md border px-2.5 py-1.5 text-left font-mono text-xs transition ${style.tile}`);
		tile.type = "button";
		tile.title = status === "Running" ? `kubectl delete pod ${name}` : name;
		tile.disabled = status === "Terminating";
		const suffix = name.slice(name.lastIndexOf("-") + 1);
		tile.append(el("span", `h-2 w-2 shrink-0 rounded-full ${style.dot}`), el("span", "truncate", `blog-…-${suffix}`), el("span", "ml-auto shrink-0 text-[10px] uppercase tracking-wide", STATUS_LABELS[status] ?? status));
		tile.dataset.pod = name;
		return tile;
	}

	function render() {
		const ready = readyCount();

		// Track how long it takes the cluster to converge after a disruption.
		if (ready < desired && healStart === null) {
			healStart = performance.now();
			healLabel.textContent = "";
		} else if (ready >= desired && healStart !== null) {
			const seconds = ((performance.now() - healStart) / 1000).toFixed(1);
			healLabel.textContent = desired > 0 ? `✅ back to ${ready}/${desired} ready in ${seconds}s` : "";
			healStart = null;
		}

		const state = desired === 0 || ready === 0 ? "down" : ready < desired ? "degraded" : "healthy";
		readyBadge.textContent = `${ready}/${desired} ready`;
		readyBadge.className = `rounded border px-2 py-0.5 text-xs ${BADGE_STYLES[state]}`;
		summary.textContent = `cluster: ${state}`;
		if (badge) {
			badge.className = `${badgeBase} ${BADGE_STYLES[state]}`;
			badgeDot.className = `status-dot h-2 w-2 rounded-full ${BADGE_DOT_STYLES[state]}`;
			badgeText.textContent = state === "down" ? "Down" : `${ready}/${desired} Running`;
		}

		replicasLabel.textContent = `replicas: ${desired}`;
		minus.disabled = desired === 0;
		plus.disabled = desired === MAX_REPLICAS;
		monkey.textContent = monkeyTimer ? "🛑 Stop the monkey" : "🐒 Unleash the monkey";
		killsLabel.textContent = `pods killed: ${kills}`;

		nodeGrid.replaceChildren();
		for (const node of [...nodes].sort()) {
			const column = el("div", "rounded-lg border border-cluster-700 bg-cluster-950/60 p-3");
			const header = el("div", "mb-2 flex items-center justify-between font-mono text-xs");
			header.append(el("span", "text-slate-300", `🖥 ${node}`), el("span", "text-slate-600", "Ready"));
			const list = el("div", "space-y-1.5 min-h-[2rem]");
			const nodePods = [...pods.values()].filter((p) => p.spec?.nodeName === node).sort((a, b) => a.metadata.name.localeCompare(b.metadata.name));
			for (const pod of nodePods) list.append(podTile(pod));
			if (!nodePods.length) list.append(el("p", "py-1.5 font-mono text-xs text-slate-600", "no pods"));
			column.append(header, list);
			nodeGrid.append(column);
		}
		const unscheduled = [...pods.values()].filter((p) => !p.spec?.nodeName);
		if (unscheduled.length) {
			const column = el("div", "space-y-1.5 sm:col-span-3");
			for (const pod of unscheduled) column.append(podTile(pod));
			nodeGrid.append(column);
		}

		eventsList.replaceChildren();
		for (const ev of events) {
			const row = el("li", "flex gap-2 whitespace-nowrap");
			row.append(
				el("span", "w-12 shrink-0 text-slate-600 sm:w-14", ev.age),
				el("span", `hidden w-14 shrink-0 sm:inline ${ev.type === "Warning" ? "text-amber-300" : "text-slate-500"}`, ev.type),
				el("span", `w-24 shrink-0 sm:w-32 ${ev.type === "Warning" ? "text-amber-300" : "text-sky-300"}`, ev.reason),
				el("span", "truncate text-slate-400", ev.message),
			);
			eventsList.append(row);
		}
		if (!events.length) eventsList.append(el("li", "text-slate-600", "waiting for events…"));
	}

	cluster.informer("nodes", (type, node) => {
		if (type === "delete") nodes.delete(node.metadata.name);
		else nodes.add(node.metadata.name);
		scheduleRender();
	});
	cluster.informer(
		"pods",
		(type, pod) => {
			if (type === "delete") pods.delete(pod.metadata.name);
			else pods.set(pod.metadata.name, pod);
			scheduleRender();
		},
		{ namespace: NAMESPACE },
	);
	cluster.informer(
		"events",
		(type, ev) => {
			if (type !== "add") return;
			const kind = ev.involvedObject?.kind?.toLowerCase() ?? "";
			events.unshift({
				age: `+${((performance.now() - bootedAt) / 1000).toFixed(1)}s`,
				type: ev.type ?? "Normal",
				reason: ev.reason ?? "",
				message: `${kind}/${ev.involvedObject?.name ?? ""}: ${ev.message ?? ""}`,
			});
			events.length = Math.min(events.length, MAX_EVENTS);
			scheduleRender();
		},
		{ namespace: NAMESPACE },
	);

	render();
}
