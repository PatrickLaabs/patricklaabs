// Entry point for the blog's interactive bits. The simulated cluster is
// several hundred KB, so it is only loaded once a visitor asks for it.
for (const root of document.querySelectorAll("[data-chaos]")) {
	const start = root.querySelector("[data-chaos-start]");
	start?.addEventListener(
		"click",
		async () => {
			const { mountChaos } = await import("./chaos.js");
			await mountChaos(root);
		},
		{ once: true },
	);
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
