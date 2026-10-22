// Live manifests: ```k8s-live code blocks in posts become editable YAML that
// readers can apply to the simulated cluster and watch come alive.
// Each block gets its own namespace, so blocks don't step on each other or
// on the blog's own deployment.

const STATUS_STYLES = {
	Running: "text-emerald-300",
	Terminating: "text-rose-300",
	CrashLoopBackOff: "text-rose-300",
	ErrImagePull: "text-rose-300",
	ImagePullBackOff: "text-rose-300",
	Error: "text-rose-300",
	Starting: "text-sky-300",
	ContainerCreating: "text-amber-300",
	Completed: "text-slate-400",
	Succeeded: "text-slate-400",
};
const MAX_OUTPUT = 8;
const GROUPS = { Ingress: "networking.k8s.io", GatewayClass: "gateway.networking.k8s.io", Gateway: "gateway.networking.k8s.io", HTTPRoute: "gateway.networking.k8s.io" };

function el(tag, className = "", text = "") {
	const node = document.createElement(tag);
	if (className) node.className = className;
	if (text) node.textContent = text;
	return node;
}

export function decorateLiveManifests(blocks) {
	blocks.forEach((code, index) => decorate(code, `live-${index + 1}`));
}

function decorate(code, namespace) {
	const pre = code.closest("pre");
	const wrapper = el("div", "live-manifest my-6 overflow-hidden rounded-lg border border-k8s/40 bg-cluster-950");
	pre.replaceWith(wrapper);

	const bar = el("div", "flex flex-wrap items-center gap-2 border-b border-cluster-700 bg-cluster-800/80 px-3 py-2 font-mono text-xs");
	bar.append(el("span", "rounded bg-k8s/20 px-1.5 py-0.5 text-k8s-light", "live"), el("span", "text-slate-500", `namespace: ${namespace}`));
	bar.append(el("span", "ml-auto hidden text-slate-600 sm:inline", "✏️ editable"));
	const apply = el("button", "rounded-md bg-k8s px-3 py-1 font-semibold text-white transition hover:bg-k8s-light disabled:cursor-wait disabled:opacity-60", "▶ kubectl apply");
	const reset = el("button", "rounded-md border border-cluster-600 px-2.5 py-1 text-slate-300 transition hover:border-rose-500/60 hover:text-white disabled:opacity-40", "↺ reset");
	apply.type = reset.type = "button";
	reset.disabled = true;
	bar.append(apply, reset);

	// Readers can edit the manifest (bump replicas, break an image tag…) and
	// apply again.
	try {
		code.contentEditable = "plaintext-only";
	} catch {
		code.contentEditable = "true";
	}
	code.spellcheck = false;
	code.setAttribute("aria-label", `Kubernetes manifest for namespace ${namespace}`);

	// Requests to try go in the manifest as comments: # curl -H "x-user: ada" shop.example.com/api/users
	const curlBar = el("div", "mt-3 hidden flex-wrap items-center gap-2");
	const curlInput = el("input", "min-w-0 flex-1 rounded border border-cluster-600 bg-cluster-900 px-2 py-0.5 font-mono text-xs text-slate-200 focus:border-k8s focus:outline-none");
	curlInput.setAttribute("aria-label", "curl command");
	curlInput.spellcheck = false;
	const curlSend = el("button", "rounded border border-cluster-600 px-2 py-0.5 text-cyan-300 transition hover:border-k8s hover:text-white", "▶ curl");
	curlSend.type = "button";
	curlSend.addEventListener("click", () => send(curlInput.value));
	curlInput.addEventListener("keydown", (event) => {
		if (event.key === "Enter") send(curlInput.value);
	});
	const curlPresets = el("div", "flex w-full flex-wrap gap-2");
	curlBar.append(el("span", "text-slate-500", "$ curl"), curlInput, curlSend, curlPresets);
	const output = el("div", "hidden border-t border-cluster-700 px-4 py-3 font-mono text-xs leading-5");
	const log = el("ol", "m-0 list-none space-y-0.5 p-0");
	const resources = el("div", "mt-3 hidden overflow-x-auto");
	const hint = el("p", "mb-0 mt-3 hidden text-slate-600", `Inspect it in the terminal: press ~ and run  kubectl get all -n ${namespace}`);
	output.append(log, resources, curlBar, hint);
	wrapper.append(bar, pre, output);

	const state = { cluster: null, m: null, pods: new Map(), services: new Map(), watching: false, applied: false, ticker: null, edge: null, requests: [] };

	function print(text, className = "text-slate-300") {
		log.append(el("li", `whitespace-pre-wrap break-words pl-0 ${className}`, text));
		while (log.childElementCount > MAX_OUTPUT) log.firstChild.remove();
		output.classList.remove("hidden");
	}

	function renderResources() {
		const { m } = state;
		resources.replaceChildren();
		const pods = [...state.pods.values()].sort((a, b) => a.metadata.name.localeCompare(b.metadata.name));
		if (pods.length) {
			const table = el("table", "m-0 w-full whitespace-nowrap text-left text-xs");
			const head = el("tr", "text-slate-500");
			for (const h of ["NAME", "READY", "STATUS", "RESTARTS", "AGE", "NODE"]) head.append(el("th", "border-0 p-0 pb-1 pr-4 font-mono text-xs font-normal normal-case tracking-normal text-slate-500", h));
			table.append(head);
			for (const pod of pods) {
				const statuses = pod.status?.containerStatuses ?? [];
				const status = m.podStatus(pod);
				const row = el("tr", "text-slate-300");
				row.append(
					el("td", "border-0 p-0 pr-4", pod.metadata.name),
					el("td", "border-0 p-0 pr-4", `${statuses.filter((s) => s.ready).length}/${pod.spec?.containers?.length ?? 0}`),
					el("td", `border-0 p-0 pr-4 ${STATUS_STYLES[status] ?? "text-slate-400"}`, status),
					el("td", "border-0 p-0 pr-4", String(statuses.reduce((n, s) => n + (s.restartCount ?? 0), 0))),
					el("td", "border-0 p-0 pr-4 text-slate-500", m.age(pod.metadata.creationTimestamp)),
					el("td", "border-0 p-0 pr-4 text-slate-500", pod.spec?.nodeName ?? "<none>"),
				);
				table.append(row);
			}
			resources.append(table);
		}
		for (const svc of state.services.values()) {
			const port = svc.spec?.ports?.find((p) => p.nodePort);
			const line = el("div", "mt-3 flex flex-wrap items-center gap-2");
			line.append(el("span", "text-slate-500", `service/${svc.metadata.name}`), el("span", "text-slate-400", `${svc.spec?.type ?? "ClusterIP"} ${svc.spec?.clusterIP ?? ""}`));
			if (port) {
				const curl = el("button", "rounded border border-cluster-600 px-2 py-0.5 text-cyan-300 transition hover:border-k8s hover:text-white", `curl ${svc.metadata.name}`);
				curl.type = "button";
				curl.addEventListener("click", () => request(svc.metadata.name, port.nodePort));
				line.append(curl);
			}
			resources.append(line);
		}
		for (const obj of state.edge ? routingObjects() : []) {
			const line = el("div", "mt-3 flex flex-wrap items-center gap-2");
			line.append(el("span", "text-slate-500", `${obj.kind.toLowerCase()}/${obj.metadata.name}`));
			if (obj.kind === "Ingress") {
				const hosts = (obj.spec?.rules ?? []).map((r) => r.host ?? "*").join(",") || "*";
				line.append(el("span", "text-slate-400", `class: ${obj.spec?.ingressClassName ?? obj.metadata.annotations?.["kubernetes.io/ingress.class"] ?? "<none>"}  hosts: ${hosts}`));
			} else if (obj.kind === "Gateway") {
				const status = m.gatewayStatusOf(obj);
				line.append(el("span", "text-slate-400", `class: ${obj.spec?.gatewayClassName ?? "<none>"}`), el("span", status.programmed ? "text-emerald-300" : "text-rose-300", `Programmed: ${status.programmed ? "True" : `False (${status.message})`}`));
			} else if (obj.kind === "HTTPRoute") {
				line.append(el("span", "text-slate-400", `hostnames: ${(obj.spec?.hostnames ?? ["*"]).join(",")}`));
				for (const parent of m.routeStatus(obj)) {
					line.append(el("span", parent.accepted ? "text-emerald-300" : "text-rose-300", `parent ${parent.name}: Accepted ${parent.accepted ? "True" : `False (${parent.reason})`}`));
				}
			}
			resources.append(line);
		}
		resources.classList.toggle("hidden", !resources.childElementCount);
	}

	function routingObjects() {
		const order = ["Gateway", "HTTPRoute", "Ingress"];
		return order.flatMap((kind) => state.m.listRouting(kind, namespace).sort((a, b) => a.metadata.name.localeCompare(b.metadata.name)));
	}

	// parseCurl understands the small part of curl the posts need:
	// -H "Name: value" headers and a URL with or without http://.
	function parseCurl(command) {
		const tokens = command.match(/"[^"]*"|'[^']*'|\S+/g)?.map((t) => t.replace(/^(["'])(.*)\1$/, "$2")) ?? [];
		const headers = {};
		let target;
		for (let i = 0; i < tokens.length; i++) {
			if (tokens[i] === "curl") continue;
			if (tokens[i] === "-H" || tokens[i] === "--header") {
				const [name, ...value] = (tokens[++i] ?? "").split(":");
				if (name) headers[name.trim()] = value.join(":").trim();
			} else if (!tokens[i].startsWith("-")) {
				target = tokens[i];
			}
		}
		if (!target) return undefined;
		const url = new URL(/^https?:\/\//.test(target) ? target : `http://${target}`);
		return { host: url.host, path: `${url.pathname}${url.search}`, headers };
	}

	async function send(command) {
		const req = parseCurl(command.trim());
		print(`$ curl ${command.trim()}`, "text-slate-500");
		if (!req) {
			print("curl: no URL specified!", "text-rose-300");
			return;
		}
		const { nodePort } = state.m.EDGES[state.edge];
		try {
			const response = await state.cluster.fetch(`http://node-1:${nodePort}${req.path}`, { headers: { ...req.headers, Host: req.host, [state.m.SCOPE_HEADER]: namespace } });
			const body = String(response.body ?? "").trim();
			const ok = response.status < 400;
			print(`HTTP ${response.status}${body ? `  ${body}` : ""}`, ok ? "text-slate-300" : "text-rose-300");
		} catch {
			print(`curl: (7) Failed to connect to ${req.host}: the ${state.edge === "ingress" ? "ingress controller" : "gateway"} is not ready yet`, "text-rose-300");
		}
	}

	function renderCurl() {
		curlPresets.replaceChildren();
		for (const command of state.requests) {
			const preset = el("button", "rounded border border-cluster-700 px-2 py-0.5 text-slate-400 transition hover:border-k8s hover:text-white", command);
			preset.type = "button";
			preset.addEventListener("click", () => {
				curlInput.value = command;
				send(command);
			});
			curlPresets.append(preset);
		}
		if (!curlInput.value || !state.requests.includes(curlInput.value)) curlInput.value = state.requests[0] ?? "";
		curlBar.classList.toggle("hidden", !state.edge);
		curlBar.classList.toggle("flex", Boolean(state.edge));
	}

	async function request(name, nodePort) {
		print(`$ curl ${name}`, "text-slate-500");
		try {
			const response = await state.cluster.fetch(`http://node-1:${nodePort}/`);
			print(String(response.body ?? "").trim() || `(HTTP ${response.status}, empty body)`);
		} catch {
			print(`curl: (7) Failed to connect to ${name}: no ready endpoints`, "text-rose-300");
		}
	}

	async function connect() {
		if (state.cluster) return;
		print("Connecting to cluster…", "text-slate-500");
		const [m, yaml] = await Promise.all([import("./cluster.js"), import("js-yaml")]);
		state.m = m;
		state.yaml = yaml;
		state.cluster = await m.bootCluster();
		log.lastChild?.remove();
	}

	function watch() {
		if (state.watching) return;
		state.watching = true;
		const track = (map) => (type, obj) => {
			if (type === "delete") map.delete(obj.metadata.name);
			else map.set(obj.metadata.name, obj);
			renderResources();
		};
		state.cluster.informer("pods", track(state.pods), { namespace });
		state.cluster.informer("services", track(state.services), { namespace });
		state.ticker = setInterval(renderResources, 1000);
	}

	function parse() {
		const docs = state.yaml.loadAll(code.textContent).filter((d) => d && typeof d === "object");
		if (!docs.length) throw new Error("no objects passed to apply");
		for (const doc of docs) {
			if (!doc.kind || !doc.metadata?.name) throw new Error("every object needs a kind and metadata.name");
			if (!["Namespace", "GatewayClass", "IngressClass"].includes(doc.kind)) doc.metadata.namespace = namespace;
		}
		return [{ apiVersion: "v1", kind: "Namespace", metadata: { name: namespace } }, ...docs];
	}

	apply.addEventListener("click", async () => {
		apply.disabled = true;
		try {
			await connect();
			print(`$ kubectl apply -f - -n ${namespace}`, "text-slate-500");
			let objects;
			try {
				objects = parse();
			} catch (err) {
				print(`error: error parsing manifest: ${err.message}`, "text-rose-300");
				return;
			}
			let routing;
			try {
				routing = objects.filter((obj) => state.m.routingKind(obj));
			} catch (err) {
				print(`error: ${err.message}`, "text-rose-300");
				return;
			}
			const core = objects.filter((obj) => !routing.includes(obj));
			const verbs = new Map();
			try {
				await state.cluster.apply(core);
				for (const obj of routing) verbs.set(obj, state.m.applyRouting(obj));
			} catch (err) {
				print(`error: ${err?.message ?? err}`, "text-rose-300");
				return;
			}
			const verb = state.applied ? "configured" : "created";
			for (const obj of objects.slice(1)) {
				const group = GROUPS[obj.kind] ? `.${GROUPS[obj.kind]}` : "";
				print(`${obj.kind.toLowerCase()}${group}/${obj.metadata.name} ${verbs.get(obj) ?? verb}`, "text-emerald-300");
			}
			if (routing.length) {
				state.edge = routing.some((obj) => obj.kind !== "Ingress" && obj.kind !== "IngressClass") ? "gateway" : "ingress";
				state.requests = [...code.textContent.matchAll(/^\s*#\s*curl\s+(.+)$/gm)].map((match) => match[1].trim());
				await state.m.ensureEdge(state.cluster, state.edge);
				renderCurl();
			}
			state.applied = true;
			reset.disabled = false;
			hint.classList.remove("hidden");
			watch();
		} catch (err) {
			console.error(err);
			print(`error: ${err?.message ?? err}`, "text-rose-300");
		} finally {
			apply.disabled = false;
		}
	});

	reset.addEventListener("click", async () => {
		reset.disabled = apply.disabled = true;
		print(`$ kubectl delete namespace ${namespace}`, "text-slate-500");
		try {
			state.m.deleteRoutingNamespace(namespace);
			await state.cluster.api.corev1.deleteNamespace({ name: namespace });
			// Wait for the namespace controller to clean up so a re-apply starts fresh.
			for (let i = 0; i < 100; i++) {
				const namespaces = await state.cluster.api.corev1.listNamespace();
				if (!namespaces.items.some((ns) => ns.metadata.name === namespace)) break;
				await new Promise((resolve) => setTimeout(resolve, 100));
			}
			print(`namespace "${namespace}" deleted`, "text-emerald-300");
			state.applied = false;
		} catch (err) {
			print(`error: ${err?.message ?? err}`, "text-rose-300");
			reset.disabled = false;
		} finally {
			apply.disabled = false;
		}
	});
}
