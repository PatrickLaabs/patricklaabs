// Entry point for the blog's interactive bits. The simulated cluster is
// several hundred KB, so it is only loaded once a visitor asks for it.
const widgets = [
	["[data-chaos]", "[data-chaos-start]", async (root) => (await import("./chaos.js")).mountChaos(root)],
	["[data-traffic]", "[data-traffic-start]", async (root) => (await import("./traffic.js")).mountTraffic(root)],
];

for (const [rootSelector, startSelector, mount] of widgets) {
	for (const root of document.querySelectorAll(rootSelector)) {
		root.querySelector(startSelector)?.addEventListener("click", () => mount(root), { once: true });
	}
}

// ```k8s-live blocks in posts become applyable manifests.
const liveBlocks = document.querySelectorAll(".markdown-content code.language-k8s-live");
if (liveBlocks.length) {
	import("./live.js").then((live) => live.decorateLiveManifests(liveBlocks));
}

// The terminal opens with ` or ~ (like a game console) or the footer button.
async function toggleTerminal() {
	const terminal = await import("./terminal.js");
	terminal.toggleTerminal();
}

document.addEventListener("keydown", (event) => {
	if (event.key !== "`" && event.key !== "~") return;
	const target = event.target;
	if (target instanceof HTMLInputElement || target instanceof HTMLTextAreaElement || target?.isContentEditable) return;
	event.preventDefault();
	toggleTerminal();
});

for (const button of document.querySelectorAll("[data-terminal-open]")) {
	button.addEventListener("click", toggleTerminal);
}
