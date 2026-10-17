// A drop-down shell with a small kubectl that talks to the simulated cluster.
// The cluster itself is only loaded on the first kubectl command.

const PROMPT = "patrick@cluster:~$ ";
const KUBECTL_COMMANDS = ["get", "delete", "scale", "logs", "exec", "describe", "apply", "top", "version", "cluster-info"];
const RESOURCES = ["pods", "nodes", "deployments", "replicasets", "services", "events", "namespaces", "all"];
const RESOURCE_ALIASES = {
	po: "pods", pod: "pods", pods: "pods",
	no: "nodes", node: "nodes", nodes: "nodes",
	deploy: "deployments", deployment: "deployments", deployments: "deployments",
	rs: "replicasets", replicaset: "replicasets", replicasets: "replicasets",
	svc: "services", service: "services", services: "services",
	ev: "events", event: "events", events: "events",
	ns: "namespaces", namespace: "namespaces", namespaces: "namespaces",
	all: "all",
};
const PROTECTED_NAMESPACES = ["blog", "kube-system", "default"];
const FILES = {
	"coffee.txt": "☕☕☕☕☕\nStatus: Running on caffeine. Liveness probe: next cup in 30m.",
	"deployment.yaml":
		"apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: blog\nspec:\n  replicas: 3\n  template:\n    spec:\n      containers:\n        - name: blog\n          image: patricklaabs/blog:1.0",
	"todo.md": "- [x] Build a blog\n- [x] Run it on Kubernetes\n- [x] Run Kubernetes in the browser\n- [ ] Write more posts",
};
const SHELL_COMMANDS = ["help", "kubectl", "k", "clear", "exit", "ls", "cat", "curl", "whoami", "hostname", "pwd", "date", "echo", "history", "sudo", "rm", "vim", "k9s", "helm", "docker"];

let terminal;

export function toggleTerminal() {
	terminal ??= createTerminal();
	terminal.toggle();
}

function el(tag, className = "", text = "") {
	const node = document.createElement(tag);
	if (className) node.className = className;
	if (text) node.textContent = text;
	return node;
}

function tokenize(line) {
	return line.match(/"[^"]*"|'[^']*'|\S+/g)?.map((t) => t.replace(/^(["'])(.*)\1$/, "$2")) ?? [];
}

function table(rows) {
	const widths = rows[0].map((_, i) => Math.max(...rows.map((r) => String(r[i]).length)));
	return rows.map((r) => r.map((c, i) => String(c).padEnd(i === r.length - 1 ? 0 : widths[i] + 3)).join("")).join("\n");
}

// parseFlags splits kubectl args into positionals and the flags we support.
function parseFlags(args) {
	const flags = { namespace: undefined, output: undefined, all: false, replicas: undefined, allNamespaces: false };
	const positional = [];
	for (let i = 0; i < args.length; i++) {
		const arg = args[i];
		const [key, inline] = arg.split("=", 2);
		const value = () => inline ?? args[++i];
		if (key === "-n" || key === "--namespace") flags.namespace = value();
		else if (key === "-o" || key === "--output") flags.output = value();
		else if (key === "--replicas") flags.replicas = value();
		else if (key === "-A" || key === "--all-namespaces") flags.allNamespaces = true;
		else if (key === "--all") flags.all = true;
		else if (key === "-w" || key === "--watch" || key === "-f" || key === "--follow") flags.watch = true;
		else if (key === "--") {
			positional.push(...args.slice(i));
			break;
		} else positional.push(arg);
	}
	return { flags, positional };
}

function createTerminal() {
	const panel = el(
		"div",
		"fixed inset-x-0 top-0 z-50 flex h-[60vh] max-h-[36rem] -translate-y-full flex-col border-b border-k8s/50 bg-cluster-950/95 font-mono text-xs shadow-2xl shadow-black/60 backdrop-blur transition-transform duration-200 sm:text-sm",
	);
	panel.setAttribute("role", "dialog");
	panel.setAttribute("aria-label", "Terminal");
	panel.hidden = true;

	const bar = el("div", "flex items-center gap-2 border-b border-cluster-700 bg-cluster-800/80 px-4 py-2 text-slate-500");
	bar.append(el("span", "h-3 w-3 rounded-full bg-rose-500/70"), el("span", "h-3 w-3 rounded-full bg-amber-400/70"), el("span", "h-3 w-3 rounded-full bg-emerald-500/70"));
	bar.append(el("span", "ml-2 truncate", "patrick@cluster: ~ — kubectl (context: prod, namespace: blog)"));
	const close = el("button", "ml-auto shrink-0 whitespace-nowrap rounded px-2 text-slate-400 hover:bg-cluster-700 hover:text-white", "esc ✕");
	close.type = "button";
	close.setAttribute("aria-label", "Close terminal");
	bar.append(close);

	const output = el("div", "flex-1 overflow-y-auto px-4 py-3 leading-relaxed text-slate-300");
	output.setAttribute("aria-live", "polite");
	const form = el("form", "flex items-center gap-2 border-t border-cluster-800 px-4 py-2.5");
	const prompt = el("label", "shrink-0 text-emerald-400", PROMPT.trim());
	const input = el("input", "min-w-0 flex-1 bg-transparent text-slate-100 caret-cyan-400 outline-none placeholder:text-slate-600");
	input.id = "terminal-input";
	prompt.htmlFor = input.id;
	input.autocomplete = "off";
	input.spellcheck = false;
	input.setAttribute("autocapitalize", "off");
	input.placeholder = "try: kubectl get pods";
	form.append(prompt, input);
	panel.append(bar, output, form);
	document.body.append(panel);

	const history = [];
	let historyIndex = 0;
	let open = false;
	let busy = false;
	let clusterModule;
	let restoreFocus;

	function print(text, className = "whitespace-pre-wrap break-words") {
		const line = el("div", className, text);
		output.append(line);
		output.scrollTop = output.scrollHeight;
	}
	const err = (text) => print(text, "whitespace-pre-wrap break-words text-rose-300");
	const ok = (text) => print(text, "whitespace-pre-wrap break-words text-emerald-300");
	const muted = (text) => print(text, "whitespace-pre-wrap break-words text-slate-500");
	// Tables keep their columns and scroll sideways on narrow screens.
	const printTable = (text) => print(text, "overflow-x-auto whitespace-pre");

	async function cluster() {
		clusterModule ??= await import("./cluster.js");
		muted("Connecting to cluster…");
		const instance = await clusterModule.bootCluster();
		output.lastChild?.remove();
		return { c: instance, m: clusterModule };
	}

	function welcome() {
		print("Welcome to patrick@cluster 👋", "text-white");
		muted("This shell runs a simulated Kubernetes cluster in your browser. Type `help` to see what it can do.");
	}

	function help() {
		print(
			[
				"kubectl (alias k):",
				"  get pods|nodes|deploy|rs|svc|events|ns|all [-n ns] [-o wide]",
				"  delete pod <name>... | delete pods --all",
				"  scale deployment/blog --replicas=<n>",
				"  logs <pod>",
				"  exec <pod> -- <cmd>        (try: printenv, cat /etc/hostname)",
				"  version | cluster-info",
				"",
				"shell:",
				"  curl blog, ls, cat <file>, whoami, history, clear, exit",
				"",
				"tips: ↑/↓ history · Tab completion · Ctrl+L clear · Esc close",
			].join("\n"),
		);
	}

	async function kubectl(args) {
		const [command, ...rest] = args;
		const { flags, positional } = parseFlags(rest);
		switch (command) {
			case undefined:
				print("kubectl controls the Kubernetes cluster manager.\n\nUsage: kubectl <command> [flags]. Type `help` for the commands this one knows.");
				return;
			case "version": {
				print("Client Version: v1.36.0-browser\nServer Version: v1.36.0-webernetes\nKustomize Version: lol no");
				return;
			}
			case "cluster-info": {
				await cluster();
				print("Kubernetes control plane is running at https://localhost (in your browser tab)\nCoreDNS is running too, probably.");
				return;
			}
			case "get":
				return await get(positional, flags);
			case "delete":
				return await del(positional, flags);
			case "scale":
				return await scale(positional, flags);
			case "logs":
				return await logs(positional, flags);
			case "exec":
				return await exec(positional);
			case "describe":
				err("error: describe is not implemented here. Try `kubectl get <resource> -o wide` instead.");
				return;
			case "apply":
				err("error: must specify one of -f and -k");
				return;
			case "top":
				err("error: Metrics API not available");
				return;
			default:
				err(`error: unknown command "${command}" for "kubectl"`);
		}
	}

	async function get(positional, flags) {
		const resource = RESOURCE_ALIASES[positional[0]];
		if (!resource) {
			err(positional[0] ? `error: the server doesn't have a resource type "${positional[0]}"` : "error: you must specify the type of resource to get. Try `kubectl get pods`.");
			return;
		}
		const { c, m } = await cluster();
		const namespace = flags.namespace ?? m.NAMESPACE;
		const wide = flags.output === "wide";
		const filter = (items) => (positional[1] ? items.filter((i) => i.metadata.name === positional[1]) : items);
		const nsColumn = flags.allNamespaces;
		const withNs = (row, item) => (nsColumn ? [item.metadata.namespace, ...row] : row);
		const header = (row) => (nsColumn ? ["NAMESPACE", ...row] : row);
		const list = async (namespaced, all) => (flags.allNamespaces ? (await all()).items : (await namespaced({ namespace })).items);

		const sections = [];
		const show = (rows, prefix = "") => {
			if (rows.length > 1) sections.push(prefix ? rows.map((r, i) => (i === 0 ? r : [prefix + r[0], ...r.slice(1)])) : rows);
		};

		if (resource === "pods" || resource === "all") {
			const pods = filter(await list((r) => c.api.corev1.listNamespacedPod(r), () => c.api.corev1.listPodForAllNamespaces()));
			const rows = [header(["NAME", "READY", "STATUS", "RESTARTS", "AGE", ...(wide ? ["IP", "NODE"] : [])])];
			for (const pod of pods) {
				const statuses = pod.status?.containerStatuses ?? [];
				const ready = statuses.filter((s) => s.ready).length;
				const total = pod.spec?.containers?.length ?? 0;
				const restarts = statuses.reduce((n, s) => n + (s.restartCount ?? 0), 0);
				rows.push(withNs([pod.metadata.name, `${ready}/${total}`, m.podStatus(pod), restarts, m.age(pod.metadata.creationTimestamp), ...(wide ? [pod.status?.podIP ?? "<none>", pod.spec?.nodeName ?? "<none>"] : [])], pod));
			}
			show(rows, resource === "all" ? "pod/" : "");
		}
		if (resource === "services" || resource === "all") {
			const services = filter(await list((r) => c.api.corev1.listNamespacedService(r), () => c.api.corev1.listServiceForAllNamespaces()));
			const rows = [header(["NAME", "TYPE", "CLUSTER-IP", "EXTERNAL-IP", "PORT(S)", "AGE"])];
			for (const svc of services) {
				const ports = (svc.spec?.ports ?? []).map((p) => `${p.port}${p.nodePort ? `:${p.nodePort}` : ""}/${p.protocol ?? "TCP"}`).join(",");
				rows.push(withNs([svc.metadata.name, svc.spec?.type ?? "ClusterIP", svc.spec?.clusterIP ?? "<none>", "<none>", ports, m.age(svc.metadata.creationTimestamp)], svc));
			}
			show(rows, resource === "all" ? "service/" : "");
		}
		if (resource === "deployments" || resource === "all") {
			const deployments = filter(await list((r) => c.api.appsv1.listNamespacedDeployment(r), () => c.api.appsv1.listDeploymentForAllNamespaces()));
			const rows = [header(["NAME", "READY", "UP-TO-DATE", "AVAILABLE", "AGE"])];
			for (const d of deployments) {
				rows.push(withNs([d.metadata.name, `${d.status?.readyReplicas ?? 0}/${d.spec?.replicas ?? 0}`, d.status?.updatedReplicas ?? 0, d.status?.availableReplicas ?? 0, m.age(d.metadata.creationTimestamp)], d));
			}
			show(rows, resource === "all" ? "deployment.apps/" : "");
		}
		if (resource === "replicasets" || resource === "all") {
			const sets = filter(await list((r) => c.api.appsv1.listNamespacedReplicaSet(r), () => c.api.appsv1.listReplicaSetForAllNamespaces()));
			const rows = [header(["NAME", "DESIRED", "CURRENT", "READY", "AGE"])];
			for (const rs of sets) {
				rows.push(withNs([rs.metadata.name, rs.spec?.replicas ?? 0, rs.status?.replicas ?? 0, rs.status?.readyReplicas ?? 0, m.age(rs.metadata.creationTimestamp)], rs));
			}
			show(rows, resource === "all" ? "replicaset.apps/" : "");
		}
		if (resource === "nodes") {
			const nodes = filter((await c.api.corev1.listNode()).items);
			const rows = [["NAME", "STATUS", "ROLES", "AGE", "VERSION"]];
			for (const node of nodes) {
				const ready = node.status?.conditions?.find((cond) => cond.type === "Ready")?.status === "True";
				rows.push([node.metadata.name, ready ? "Ready" : "NotReady", "<none>", m.age(node.metadata.creationTimestamp), node.status?.nodeInfo?.kubeletVersion || "v1.36.0-webernetes"]);
			}
			show(rows);
		}
		if (resource === "namespaces") {
			const namespaces = filter((await c.api.corev1.listNamespace()).items);
			show([["NAME", "STATUS", "AGE"], ...namespaces.map((ns) => [ns.metadata.name, ns.status?.phase ?? "Active", m.age(ns.metadata.creationTimestamp)])]);
		}
		if (resource === "events") {
			const events = await list((r) => c.api.corev1.listNamespacedEvent(r), () => c.api.corev1.listEventForAllNamespaces());
			const time = (e) => new Date(e.lastTimestamp ?? e.eventTime ?? e.metadata.creationTimestamp).getTime();
			const rows = [header(["LAST SEEN", "TYPE", "REASON", "OBJECT", "MESSAGE"])];
			for (const e of events.sort((a, b) => time(a) - time(b)).slice(-20)) {
				rows.push(withNs([m.age(time(e)), e.type ?? "Normal", e.reason ?? "", `${e.involvedObject?.kind?.toLowerCase()}/${e.involvedObject?.name}`, e.message ?? ""], e));
			}
			show(rows);
		}

		if (flags.watch) muted("(watching is not supported here – the Chaos Monkey section above is a live view)");
		if (!sections.length) {
			if (positional[1]) err(`Error from server (NotFound): ${resource} "${positional[1]}" not found`);
			else muted(`No resources found in ${flags.allNamespaces ? "any" : namespace} namespace.`);
			return;
		}
		printTable(sections.map(table).join("\n\n"));
	}

	async function del(positional, flags) {
		const resource = RESOURCE_ALIASES[positional[0]];
		const names = positional.slice(1);
		if (resource === "namespaces") {
			const name = names[0] ?? "";
			if (PROTECTED_NAMESPACES.includes(name)) {
				err(`Error from server (Forbidden): admission webhook "no-fun-allowed.patricklaabs.dev" denied the request: nice try 😏 namespace "${name}" is load-bearing`);
				return;
			}
			err(`Error from server (NotFound): namespaces "${name}" not found`);
			return;
		}
		if (resource === "deployments" || resource === "services") {
			err(`Error from server (Forbidden): admission webhook "no-fun-allowed.patricklaabs.dev" denied the request: deleting the ${resource.slice(0, -1)} would take down the blog. Try killing pods instead, they come back.`);
			return;
		}
		if (resource === "nodes") {
			err("Error from server (Forbidden): nodes are cattle, not pets – but these ones are rented. Denied.");
			return;
		}
		if (resource !== "pods") {
			err(positional[0] ? `error: the server doesn't have a resource type "${positional[0]}"` : "error: you must specify the type of resource to delete");
			return;
		}
		const { c, m } = await cluster();
		const namespace = flags.namespace ?? m.NAMESPACE;
		let targets = names;
		if (flags.all) targets = (await c.api.corev1.listNamespacedPod({ namespace })).items.filter((p) => !p.metadata.deletionTimestamp).map((p) => p.metadata.name);
		if (!targets.length) {
			err("error: resource(s) were provided, but no name was specified");
			return;
		}
		for (const name of targets) {
			try {
				await c.api.corev1.deleteNamespacedPod({ namespace, name });
				ok(`pod "${name}" deleted`);
			} catch {
				err(`Error from server (NotFound): pods "${name}" not found`);
			}
		}
		if (flags.all) muted("💀 Everything's gone. Don't worry, the ReplicaSet is already on it.");
	}

	async function scale(positional, flags) {
		const target = positional[0] === "deployment" || positional[0] === "deploy" ? `deployment/${positional[1]}` : positional[0];
		const replicas = Number(flags.replicas);
		if (!target || flags.replicas === undefined) {
			err("error: Required flag(s) \"replicas\" not set. Usage: kubectl scale deployment/blog --replicas=5");
			return;
		}
		const { c, m } = await cluster();
		if (target !== `deployment/${m.DEPLOYMENT}`) {
			err(`Error from server (NotFound): ${target.replace("/", 's.apps "')}" not found`);
			return;
		}
		if (!Number.isInteger(replicas) || replicas < 0) {
			err(`error: invalid --replicas value "${flags.replicas}"`);
			return;
		}
		if (replicas > 20) {
			err("error: exceeded quota: your-browser-tab, requested: pods=" + replicas + ", limited: pods=20 (have mercy on your laptop fan)");
			return;
		}
		await m.scaleDeployment(c, replicas);
		ok(`deployment.apps/${m.DEPLOYMENT} scaled`);
		if (replicas === 0) muted("The blog is now down. Very cloud native of you. 🫠");
	}

	async function logs(positional) {
		const name = positional[0];
		if (!name) {
			err("error: expected 'logs POD_NAME'. Try `kubectl get pods` to find one.");
			return;
		}
		const { c, m } = await cluster();
		try {
			await c.api.corev1.readNamespacedPod({ namespace: m.NAMESPACE, name });
		} catch {
			err(`Error from server (NotFound): pods "${name}" not found`);
			return;
		}
		print((m.podLogs(name) ?? []).join("\n") || "(no logs yet)", "whitespace-pre-wrap break-words text-slate-400");
	}

	async function exec(positional) {
		const name = positional.find((p) => !p.startsWith("-"));
		const dash = positional.indexOf("--");
		const command = dash >= 0 ? positional.slice(dash + 1) : [];
		if (!name || !command.length) {
			err("error: you must specify at least one command for the container. Usage: kubectl exec <pod> -- <cmd>");
			return;
		}
		if (["sh", "bash", "zsh"].includes(command[0]) && command.length === 1) {
			err("error: interactive shells are not supported. Try `kubectl exec <pod> -- printenv`.");
			return;
		}
		const { c, m } = await cluster();
		try {
			const result = await c.exec(m.NAMESPACE, name, undefined, command);
			if (result.stdout) print(result.stdout.trimEnd());
			if (result.stderr) err(result.stderr.trimEnd());
			if (result.exitCode !== 0) muted(`command terminated with exit code ${result.exitCode}`);
		} catch (e) {
			err(`error: ${e?.message ?? e}`);
		}
	}

	async function curl(args) {
		const target = args.find((a) => !a.startsWith("-"));
		if (!target) {
			err("curl: try 'curl blog'");
			return;
		}
		if (!/^(https?:\/\/)?(blog|localhost|node-\d)(\.blog(\.svc(\.cluster\.local)?)?)?(:\d+)?(\/.*)?$/.test(target)) {
			err(`curl: (6) Could not resolve host: ${target.replace(/^https?:\/\//, "").split("/")[0]} (this cluster has no internet, only vibes)`);
			return;
		}
		const { c, m } = await cluster();
		const path = target.replace(/^(https?:\/\/)?[^/]+/, "") || "/";
		try {
			const response = await c.fetch(`${m.SERVICE_URL}${path}`);
			print(String(response.body ?? "").trimEnd());
		} catch (e) {
			err(`curl: (7) Failed to connect to blog port 80: ${e?.message ?? "Connection refused"}`);
		}
	}

	async function run(line) {
		const args = tokenize(line);
		const [command, ...rest] = args;
		switch (command) {
			case undefined:
				return;
			case "help":
				return help();
			case "kubectl":
			case "k":
				return await kubectl(rest);
			case "clear":
				output.replaceChildren();
				return;
			case "exit":
			case "logout":
				hide();
				return;
			case "ls":
				print(Object.keys(FILES).join("  "));
				return;
			case "cat":
				if (FILES[rest[0]]) print(FILES[rest[0]]);
				else err(`cat: ${rest[0] ?? ""}: No such file or directory`);
				return;
			case "curl":
				return await curl(rest);
			case "whoami":
				print("patrick");
				return;
			case "hostname":
				print("cluster");
				return;
			case "pwd":
				print("/home/patrick");
				return;
			case "date":
				print(new Date().toString());
				return;
			case "echo":
				print(rest.join(" "));
				return;
			case "history":
				print(history.map((h, i) => `${String(i + 1).padStart(4)}  ${h}`).join("\n"));
				return;
			case "sudo":
				err("patrick is not in the sudoers file. This incident will be reported. 🚨");
				return;
			case "rm":
				if (rest.includes("-rf") || rest.includes("-fr")) print("🔥 This is fine. 🔥", "text-amber-300");
				else err("rm: permission denied (files are immutable, it's a container)");
				return;
			case "vim":
			case "vi":
			case "nano":
			case "emacs":
				print("Opening editor… just kidding. How do you even exit vim? (:q! — you're welcome)", "whitespace-pre-wrap break-words text-amber-300");
				return;
			case "k9s":
				print("k9s would be nice, wouldn't it? Maybe in a future PR. 🐶");
				return;
			case "helm":
				print('Error: INSTALLATION FAILED: chart "happiness" requires a working Chart.yaml and at least 3 coffees');
				return;
			case "docker":
				print("Docker? In this economy? We use containerd here… well, a TypeScript simulation of it. Try kubectl.");
				return;
			default:
				err(`${command}: command not found. Type \`help\` for available commands.`);
		}
	}

	async function complete() {
		const value = input.value;
		const tokens = value.split(/\s+/);
		const current = tokens.at(-1);
		let candidates = [];
		if (tokens.length === 1) candidates = SHELL_COMMANDS;
		else if (["kubectl", "k"].includes(tokens[0])) {
			if (tokens.length === 2) candidates = KUBECTL_COMMANDS;
			else if (tokens.length === 3 && ["get", "delete"].includes(tokens[1])) candidates = RESOURCES;
			else if (["logs", "exec"].includes(tokens[1]) || (tokens[1] === "delete" && RESOURCE_ALIASES[tokens[2]] === "pods")) {
				const { c, m } = await cluster();
				candidates = (await c.api.corev1.listNamespacedPod({ namespace: m.NAMESPACE })).items.map((p) => p.metadata.name);
			} else if (tokens[1] === "scale") candidates = tokens.length === 3 ? ["deployment/blog"] : ["--replicas="];
		} else if (tokens[0] === "cat") candidates = Object.keys(FILES);
		else if (tokens[0] === "curl") candidates = ["blog"];

		const matches = candidates.filter((c) => c.startsWith(current));
		if (matches.length === 1) {
			tokens[tokens.length - 1] = matches[0];
			input.value = tokens.join(" ") + (matches[0].endsWith("=") ? "" : " ");
		} else if (matches.length > 1) {
			print(PROMPT + value, "whitespace-pre-wrap break-words text-slate-500");
			print(matches.join("  "));
			let prefix = matches[0];
			for (const match of matches) while (!match.startsWith(prefix)) prefix = prefix.slice(0, -1);
			tokens[tokens.length - 1] = prefix;
			input.value = tokens.join(" ");
		}
	}

	form.addEventListener("submit", async (event) => {
		event.preventDefault();
		if (busy) return;
		const line = input.value.trim();
		input.value = "";
		print(PROMPT + line, "whitespace-pre-wrap break-words text-slate-500");
		if (line) {
			history.push(line);
			historyIndex = history.length;
		}
		busy = true;
		try {
			await run(line);
		} catch (e) {
			console.error(e);
			err(`error: ${e?.message ?? e}`);
		} finally {
			busy = false;
		}
	});

	input.addEventListener("keydown", (event) => {
		if (event.key === "ArrowUp" || event.key === "ArrowDown") {
			event.preventDefault();
			historyIndex = Math.min(history.length, Math.max(0, historyIndex + (event.key === "ArrowUp" ? -1 : 1)));
			input.value = history[historyIndex] ?? "";
		} else if (event.key === "Tab") {
			event.preventDefault();
			complete().catch((e) => console.error(e));
		} else if (event.key === "l" && event.ctrlKey) {
			event.preventDefault();
			output.replaceChildren();
		} else if (event.key === "Escape" || (event.key === "`" && !input.value)) {
			event.preventDefault();
			hide();
		}
	});
	close.addEventListener("click", () => hide());

	function show() {
		restoreFocus = document.activeElement;
		panel.hidden = false;
		requestAnimationFrame(() => panel.classList.remove("-translate-y-full"));
		open = true;
		if (!output.childElementCount) welcome();
		input.focus();
	}

	function hide() {
		panel.classList.add("-translate-y-full");
		open = false;
		setTimeout(() => {
			if (!open) panel.hidden = true;
		}, 200);
		restoreFocus?.focus?.();
	}

	return { toggle: () => (open ? hide() : show()) };
}
