// Ingress and Gateway API for the simulated cluster. webernetes only knows
// the core and apps APIs, so the routing objects live in a small store here,
// and two edge proxies running as pods read from it: one plays ingress-nginx,
// the other a Gateway API implementation. Both forward to Services over the
// cluster network, like the real thing.
import { BaseImage } from "@ngrok/webernetes";

export const INGRESS_CLASS = "nginx";
export const GATEWAY_CLASS = "webernetes";

const KINDS = {
	"networking.k8s.io/v1/Ingress": "Ingress",
	"networking.k8s.io/v1/IngressClass": "IngressClass",
	"gateway.networking.k8s.io/v1/GatewayClass": "GatewayClass",
	"gateway.networking.k8s.io/v1/Gateway": "Gateway",
	"gateway.networking.k8s.io/v1/HTTPRoute": "HTTPRoute",
	"gateway.networking.k8s.io/v1beta1/HTTPRoute": "HTTPRoute",
};
const CLUSTER_SCOPED = ["IngressClass", "GatewayClass"];

// The edge proxies, one NodePort each. Requests from the live manifests go to
// whichever one the manifest needs.
export const EDGES = {
	ingress: { namespace: "ingress-nginx", name: "ingress-nginx-controller", nodePort: 30180 },
	gateway: { namespace: "gateway-system", name: "webernetes-gateway", nodePort: 30280 },
};

// Every live manifest gets its own entry point, as if each Gateway had its own
// address. The widget names the namespace in this header, and the proxies
// only route to objects in it and strip the header before forwarding.
export const SCOPE_HEADER = "X-Live-Namespace";

const objects = new Map();
const subscribers = new Set();

const key = (kind, namespace, name) => `${kind}/${CLUSTER_SCOPED.includes(kind) ? "" : namespace}/${name}`;

function store(kind, obj) {
	objects.set(key(kind, obj.metadata.namespace, obj.metadata.name), obj);
}

// The classes the two controllers own are installed with the cluster.
store("IngressClass", { apiVersion: "networking.k8s.io/v1", kind: "IngressClass", metadata: { name: INGRESS_CLASS, creationTimestamp: new Date().toISOString() }, spec: { controller: "k8s.io/ingress-nginx" } });
store("GatewayClass", { apiVersion: "gateway.networking.k8s.io/v1", kind: "GatewayClass", metadata: { name: GATEWAY_CLASS, creationTimestamp: new Date().toISOString() }, spec: { controllerName: "patricklaabs.dev/webernetes-gateway" } });

function notify() {
	for (const fn of subscribers) fn();
}

export function subscribe(fn) {
	subscribers.add(fn);
	return () => subscribers.delete(fn);
}

// routingKind returns the kind if obj belongs in this store. A known kind in an
// unknown version fails like it does on a real API server.
export function routingKind(obj) {
	const kind = KINDS[`${obj.apiVersion}/${obj.kind}`];
	if (kind) return kind;
	if (Object.values(KINDS).includes(obj.kind)) {
		throw new Error(`resource mapping not found for name: "${obj.metadata?.name}" namespace: "${obj.metadata?.namespace ?? ""}" from "STDIN": no matches for kind "${obj.kind}" in version "${obj.apiVersion}"\nensure CRDs are installed first`);
	}
	return undefined;
}

export function applyRouting(obj) {
	const kind = routingKind(obj);
	const k = key(kind, obj.metadata.namespace, obj.metadata.name);
	const existing = objects.get(k);
	const next = structuredClone(obj);
	next.metadata.creationTimestamp = existing?.metadata.creationTimestamp ?? new Date().toISOString();
	objects.set(k, next);
	notify();
	return existing ? "configured" : "created";
}

export function deleteRoutingNamespace(namespace) {
	for (const [k, obj] of objects) {
		if (obj.metadata.namespace === namespace) objects.delete(k);
	}
	notify();
}

export function listRouting(kind, namespace) {
	return [...objects.values()].filter((o) => o.kind === kind && (namespace === undefined || CLUSTER_SCOPED.includes(kind) || o.metadata.namespace === namespace));
}

const get = (kind, namespace, name) => objects.get(key(kind, namespace, name));

// Hostnames ------------------------------------------------------------------

function hostMatches(pattern, host) {
	if (!pattern) return true;
	if (pattern.startsWith("*.")) return host.endsWith(pattern.slice(1)) && host.split(".").length > pattern.split(".").length - 1;
	return pattern === host;
}

// Two hostnames intersect if some host matches both of them.
function hostsIntersect(a, b) {
	if (!a || !b) return true;
	return hostMatches(a, b.replace(/^\*\./, "x.")) || hostMatches(b, a.replace(/^\*\./, "x."));
}

// Ingress (as ingress-nginx does it) --------------------------------------------

function ingressClassOf(ing) {
	return ing.spec?.ingressClassName ?? ing.metadata.annotations?.["kubernetes.io/ingress.class"];
}

const NGINX = "nginx.ingress.kubernetes.io/";

export function routeIngress(request, scope) {
	const host = request.host.replace(/:\d+$/, "");
	const path = request.url.pathname;
	const ingresses = listRouting("Ingress", scope).filter((ing) => ingressClassOf(ing) === INGRESS_CLASS);

	const rules = [];
	for (const ing of ingresses) {
		for (const rule of ing.spec?.rules ?? []) {
			if (hostMatches(rule.host, host)) rules.push({ ing, rule });
		}
	}
	// ingress-nginx renders one nginx server block per host. As soon as one
	// Ingress for a host uses regex or rewrite-target, every path of that host
	// is a case-insensitive regex. A classic surprise.
	const regex = rules.some(({ ing }) => ing.metadata.annotations?.[`${NGINX}use-regex`] === "true" || ing.metadata.annotations?.[`${NGINX}rewrite-target`]);

	let best;
	for (const { ing, rule } of rules) {
		for (const p of rule.http?.paths ?? []) {
			const value = p.path ?? "/";
			let match;
			if (regex) {
				try {
					match = new RegExp(`^${value}`, "i").exec(path);
				} catch {
					continue;
				}
			} else if (p.pathType === "Exact") {
				match = value === path ? [path] : null;
			} else {
				match = prefixMatch(value, path) || (p.pathType === "ImplementationSpecific" && path.startsWith(value)) ? [path] : null;
			}
			if (!match) continue;
			const score = [p.pathType === "Exact" && !regex ? 1 : 0, value.length];
			if (!best || score[0] > best.score[0] || (score[0] === best.score[0] && score[1] > best.score[1])) best = { ing, p, match, score };
		}
	}
	if (!best) {
		return { status: 404, body: "<html><head><title>404 Not Found</title></head><body><center><h1>404 Not Found</h1></center><hr><center>nginx</center></body></html>" };
	}

	const annotations = best.ing.metadata.annotations ?? {};
	let target = path;
	const rewrite = annotations[`${NGINX}rewrite-target`];
	if (rewrite) target = rewrite.replace(/\$(\d)/g, (_, i) => best.match[Number(i)] ?? "");
	if (!target.startsWith("/")) target = `/${target}`;

	const svc = best.p.backend?.service;
	if (!svc?.name) return { status: 503, body: "503 Service Temporarily Unavailable\nnginx" };
	return { backend: { namespace: best.ing.metadata.namespace, service: svc.name, port: svc.port?.number ?? 80 }, path: target, headers: {} };
}

// Gateway API -----------------------------------------------------------------

// prefixMatch implements Kubernetes path prefixes: they match whole path
// elements, so /api matches /api and /api/users but not /apiv2.
function prefixMatch(prefix, path) {
	const p = prefix.replace(/\/+$/, "");
	return p === "" || path === p || path.startsWith(`${p}/`);
}

function gatewayStatus(gw) {
	if (gw.spec?.gatewayClassName !== GATEWAY_CLASS) {
		return { programmed: false, reason: "Pending", message: `GatewayClass "${gw.spec?.gatewayClassName ?? ""}" not found` };
	}
	return { programmed: true, reason: "Programmed", message: "" };
}

// parentStatus decides whether a route attaches to one of its parents, and
// which listeners it gets.
function parentStatus(route, ref) {
	const namespace = ref.namespace ?? route.metadata.namespace;
	if ((ref.kind ?? "Gateway") !== "Gateway") return { accepted: false, reason: "InvalidKind", listeners: [] };
	const gw = get("Gateway", namespace, ref.name);
	if (!gw || !gatewayStatus(gw).programmed) return { accepted: false, reason: "NoMatchingParent", message: `Gateway ${namespace}/${ref.name} not found`, listeners: [] };
	let listeners = gw.spec?.listeners ?? [];
	if (ref.sectionName) listeners = listeners.filter((l) => l.name === ref.sectionName);
	if (!listeners.length) return { accepted: false, reason: "NoMatchingParent", message: `listener "${ref.sectionName}" not found`, listeners: [] };
	listeners = listeners.filter((l) => (l.allowedRoutes?.namespaces?.from ?? "Same") === "All" || namespace === route.metadata.namespace);
	if (!listeners.length) return { accepted: false, reason: "NotAllowedByListeners", listeners: [] };
	const hostnames = route.spec?.hostnames ?? [];
	listeners = listeners.filter((l) => !hostnames.length || hostnames.some((h) => hostsIntersect(h, l.hostname)));
	if (!listeners.length) return { accepted: false, reason: "NoMatchingListenerHostname", listeners: [] };
	return { accepted: true, reason: "Accepted", listeners };
}

export function routeStatus(route) {
	const refs = route.spec?.parentRefs ?? [];
	if (!refs.length) return [{ name: "<none>", accepted: false, reason: "NoMatchingParent" }];
	return refs.map((ref) => ({ name: ref.name, ...parentStatus(route, ref) }));
}

export function gatewayStatusOf(gw) {
	return gatewayStatus(gw);
}

function matchScore(match) {
	const type = match.path?.type ?? "PathPrefix";
	const value = match.path?.value ?? "/";
	return [type === "Exact" ? 1 : 0, value.length, match.method ? 1 : 0, match.headers?.length ?? 0];
}

function compare(a, b) {
	for (let i = 0; i < a.length; i++) if (a[i] !== b[i]) return a[i] - b[i];
	return 0;
}

function header(request, name) {
	const k = Object.keys(request.header).find((h) => h.toLowerCase() === name.toLowerCase());
	return k ? request.header[k][0] : undefined;
}

function matches(match, request) {
	const path = request.url.pathname;
	const type = match.path?.type ?? "PathPrefix";
	const value = match.path?.value ?? "/";
	if (type === "Exact" && path !== value) return false;
	if (type === "PathPrefix" && !prefixMatch(value, path)) return false;
	if (type === "RegularExpression" && !new RegExp(`^${value}$`).test(path)) return false;
	if (match.method && match.method !== request.method) return false;
	for (const h of match.headers ?? []) {
		const actual = header(request, h.name);
		if (actual === undefined) return false;
		if ((h.type ?? "Exact") === "Exact" ? actual !== h.value : !new RegExp(`^${h.value}$`).test(actual)) return false;
	}
	for (const q of match.queryParams ?? []) {
		if (request.url.searchParams.get(q.name) !== q.value) return false;
	}
	return true;
}

function pickBackend(refs) {
	const weighted = refs.map((r) => ({ ref: r, weight: r.weight ?? 1 })).filter((r) => r.weight > 0);
	const total = weighted.reduce((n, r) => n + r.weight, 0);
	if (!total) return undefined;
	let roll = Math.random() * total;
	for (const r of weighted) {
		roll -= r.weight;
		if (roll < 0) return r.ref;
	}
	return weighted.at(-1).ref;
}

export function routeGateway(request, scope) {
	const host = request.host.replace(/:\d+$/, "");
	let best;
	for (const route of listRouting("HTTPRoute", scope)) {
		const attached = routeStatus(route).filter((s) => s.accepted);
		if (!attached.length) continue;
		const hostnames = route.spec?.hostnames ?? [];
		if (hostnames.length && !hostnames.some((h) => hostMatches(h, host))) continue;
		if (!attached.some((s) => s.listeners.some((l) => hostMatches(l.hostname, host)))) continue;
		for (const rule of route.spec?.rules ?? [{}]) {
			for (const match of rule.matches?.length ? rule.matches : [{}]) {
				if (!matches(match, request)) continue;
				const score = matchScore(match);
				// On a tie the route applied first wins.
				if (!best || compare(score, best.score) > 0) best = { route, rule, match, score };
			}
		}
	}
	if (!best) return { status: 404, body: "404 Not Found: no HTTPRoute matches this request" };

	let path = request.url.pathname;
	let headers = {};
	for (const filter of best.rule.filters ?? []) {
		if (filter.type === "RequestRedirect") {
			const r = filter.requestRedirect ?? {};
			const location = `${r.scheme ?? "http"}://${r.hostname ?? host}${r.port ? `:${r.port}` : ""}${r.path?.replaceFullPath ?? path}`;
			return { status: r.statusCode ?? 302, body: `redirecting to ${location}`, header: { Location: [location] } };
		}
		if (filter.type === "URLRewrite" && filter.urlRewrite?.path) {
			const rw = filter.urlRewrite.path;
			if (rw.type === "ReplaceFullPath") path = rw.replaceFullPath ?? "/";
			if (rw.type === "ReplacePrefixMatch") {
				const prefix = (best.match.path?.value ?? "/").replace(/\/+$/, "");
				const rest = path.slice(prefix.length);
				path = `${(rw.replacePrefixMatch ?? "/").replace(/\/+$/, "")}${rest}` || "/";
				if (!path.startsWith("/")) path = `/${path}`;
			}
		}
		if (filter.type === "RequestHeaderModifier") {
			const m = filter.requestHeaderModifier ?? {};
			for (const h of [...(m.set ?? []), ...(m.add ?? [])]) headers[h.name] = h.value;
			for (const name of m.remove ?? []) headers[name] = null;
		}
	}

	const ref = pickBackend(best.rule.backendRefs ?? []);
	if (!ref) return { status: 500, body: "500 Internal Server Error: no backendRefs" };
	return { backend: { namespace: ref.namespace ?? best.route.metadata.namespace, service: ref.name, port: ref.port ?? 80 }, path, headers };
}

// The proxies -------------------------------------------------------------------

function proxyImage(imageName, imageVersion, router, unavailable, log) {
	return class extends BaseImage {
		static imageName = imageName;
		static imageVersion = imageVersion;
		defaultCommand = ["proxy"];

		async exec(ctx, argv) {
			if (argv[0] !== "proxy") return await super.exec(ctx, argv);
			log(ctx, `${imageName}:${imageVersion} listening on :80`);
			ctx.listenHttp(80, async (_requestCtx, request) => {
				const scope = header(request, SCOPE_HEADER);
				const decision = router(request, scope);
				const line = (status, upstream = "-") => log(ctx, `${request.host} "${request.method} ${request.url.pathname}" ${status} upstream=${upstream}`);
				if (!decision.backend) {
					line(decision.status);
					return decision;
				}
				const headers = {};
				for (const [name, values] of Object.entries(request.header)) {
					if (!["x-webernetes-request-id", SCOPE_HEADER.toLowerCase()].includes(name.toLowerCase())) headers[name] = values[0];
				}
				for (const [name, value] of Object.entries(decision.headers)) {
					for (const k of Object.keys(headers)) if (k.toLowerCase() === name.toLowerCase()) delete headers[k];
					if (value !== null) headers[name] = value;
				}
				const { service, namespace, port } = decision.backend;
				const upstream = `${service}.${namespace}:${port}${decision.path}`;
				try {
					const response = await ctx.fetch(`http://${service}.${namespace}.svc.cluster.local:${port}${decision.path}${request.url.search}`, { method: request.method, headers });
					line(response.status, upstream);
					return { status: response.status, body: response.body };
				} catch {
					line(503, upstream);
					return { status: 503, body: unavailable };
				}
			});
			return await ctx.waitUntilKilled();
		}
	};
}

export function edgeImages(log) {
	return [
		proxyImage("registry.k8s.io/ingress-nginx/controller", "v1.13.3", routeIngress, "503 Service Temporarily Unavailable\nnginx", log),
		proxyImage("patricklaabs/gateway", "1.0", routeGateway, "no healthy upstream", log),
	];
}

export function edgeManifests(edge) {
	const { namespace, name, nodePort } = EDGES[edge];
	const image = edge === "ingress" ? "registry.k8s.io/ingress-nginx/controller:v1.13.3" : "patricklaabs/gateway:1.0";
	return [
		{ apiVersion: "v1", kind: "Namespace", metadata: { name: namespace } },
		{
			apiVersion: "apps/v1",
			kind: "Deployment",
			metadata: { name, namespace },
			spec: {
				replicas: 1,
				selector: { matchLabels: { app: name } },
				template: { metadata: { labels: { app: name } }, spec: { containers: [{ name: "proxy", image, ports: [{ containerPort: 80 }] }] } },
			},
		},
		{
			apiVersion: "v1",
			kind: "Service",
			metadata: { name, namespace },
			spec: { type: "NodePort", selector: { app: name }, ports: [{ port: 80, targetPort: 80, nodePort }] },
		},
	];
}
