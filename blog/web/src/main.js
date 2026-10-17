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
