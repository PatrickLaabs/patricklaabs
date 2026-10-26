// In-place pod resize for the simulated cluster, with the v1.37 alpha
// scheduler preemption for resizes (InPlacePodVerticalScalingSchedulerPreemption)
// switched on. webernetes doesn't track resources, so this module keeps the
// CPU the kubelet has allocated to each pod and decides, like the kubelet and
// the scheduler would, whether a resize fits on the node.
//
// PriorityClasses live here too, because webernetes doesn't know them and
// preemption needs pod priorities.

const PRIORITY_KIND = "scheduling.k8s.io/v1/PriorityClass";

// Allocatable CPU per node, in millicores. Only pods with CPU requests count.
export const NODE_CPU = 2000;

const RECONCILE_MS = 500;
const IN_PROGRESS_MS = 1500;

const priorityClasses = new Map();
// Per pod (namespace/name): the uid it belongs to, the CPU request the kubelet
// has allocated, the one the pod asks for, and the resize conditions.
const allocations = new Map();
const subscribers = new Set();
let controller;

const key = (namespace, name) => `${namespace}/${name}`;

function notify() {
	for (const fn of subscribers) fn();
}

export function subscribe(fn) {
	subscribers.add(fn);
	return () => subscribers.delete(fn);
}

// Quantities ------------------------------------------------------------------------

// millicores parses a CPU quantity like "500m", "1.5" or 2.
export function millicores(quantity) {
	if (quantity === undefined || quantity === null || quantity === "") return 0;
	const text = String(quantity);
	if (text.endsWith("m")) return Number(text.slice(0, -1));
	return Math.round(Number(text) * 1000);
}

export function formatCPU(m) {
	return m % 1000 === 0 ? String(m / 1000) : `${m}m`;
}

function requestedCPU(containers) {
	return (containers ?? []).reduce((sum, c) => sum + millicores(c.resources?.requests?.cpu ?? c.resources?.limits?.cpu), 0);
}

// PriorityClasses ---------------------------------------------------------------------

export function isPriorityClass(obj) {
	if (obj.kind !== "PriorityClass") return false;
	if (`${obj.apiVersion}/${obj.kind}` === PRIORITY_KIND) return true;
	throw new Error(`resource mapping not found for name: "${obj.metadata?.name}" namespace: "" from "STDIN": no matches for kind "${obj.kind}" in version "${obj.apiVersion}"\nensure CRDs are installed first`);
}

export function applyPriorityClass(obj) {
	if (!Number.isInteger(obj.value)) throw new Error(`The PriorityClass "${obj.metadata.name}" is invalid: value: Required value`);
	const existing = priorityClasses.get(obj.metadata.name);
	if (existing && existing.value !== obj.value) throw new Error(`The PriorityClass "${obj.metadata.name}" is invalid: value: Forbidden: may not be changed in an update.`);
	priorityClasses.set(obj.metadata.name, { ...structuredClone(obj), metadata: { ...obj.metadata, creationTimestamp: existing?.metadata.creationTimestamp ?? new Date().toISOString() } });
	return existing ? "unchanged" : "created";
}

export function listPriorityClasses() {
	return [...priorityClasses.values()];
}

// checkPodPriority fails like the Priority admission plugin for pods that name a
// PriorityClass that doesn't exist.
export function checkPodPriority(pod) {
	const name = pod.spec?.priorityClassName;
	if (name && !priorityClasses.has(name)) throw new Error(`pods "${pod.metadata.name}" is forbidden: no PriorityClass with name ${name} was found`);
}

export function priorityOf(pod) {
	return priorityClasses.get(pod.spec?.priorityClassName)?.value ?? 0;
}

// Allocations -------------------------------------------------------------------------

function allocationOf(pod) {
	const k = key(pod.metadata.namespace, pod.metadata.name);
	let a = allocations.get(k);
	if (!a || a.uid !== pod.metadata.uid) {
		const cpu = requestedCPU(pod.spec?.containers);
		a = { uid: pod.metadata.uid, allocated: cpu, desired: cpu, containers: Object.fromEntries((pod.spec?.containers ?? []).map((c) => [c.name, millicores(c.resources?.requests?.cpu ?? c.resources?.limits?.cpu)])), pending: undefined, inProgressUntil: 0 };
		allocations.set(k, a);
	}
	return a;
}

// resizeInfo is what the live widget shows for a pod.
export function resizeInfo(pod) {
	const a = allocationOf(pod);
	const condition = a.pending ? { type: "PodResizePending", ...a.pending } : Date.now() < a.inProgressUntil ? { type: "PodResizeInProgress" } : undefined;
	return { allocated: a.allocated, desired: a.desired, priority: priorityOf(pod), condition };
}

export function hasResources(pods) {
	return pods.some((pod) => allocationOf(pod).allocated > 0 || pod.spec?.priorityClassName);
}

const live = (pod) => pod.status?.phase !== "Succeeded" && pod.status?.phase !== "Failed";

// usedOnNode adds up the CPU allocated to every other pod on the node. Pods
// that are shutting down still hold their CPU until they are gone.
function usedOnNode(pods, self) {
	return pods.filter((p) => p.spec?.nodeName === self.spec?.nodeName && p.metadata.uid !== self.metadata.uid && live(p)).reduce((sum, p) => sum + allocationOf(p).allocated, 0);
}

// patchResize handles `kubectl patch pod --subresource resize`. It only takes
// container resources, like the real subresource.
export async function patchResize(cluster, namespace, name, patch) {
	let pod;
	try {
		pod = await cluster.api.corev1.readNamespacedPod({ namespace, name });
	} catch {
		throw new Error(`Error from server (NotFound): pods "${name}" not found`);
	}
	let body;
	try {
		body = typeof patch === "string" ? JSON.parse(patch) : patch;
	} catch (err) {
		throw new Error(`error: unable to parse "${patch}": ${err.message}`);
	}
	const extra = Object.keys(body ?? {}).filter((k) => k !== "spec").concat(Object.keys(body?.spec ?? {}).filter((k) => k !== "containers"));
	if (extra.length || !Array.isArray(body?.spec?.containers)) {
		throw new Error(`The Pod "${name}" is invalid: spec: Forbidden: only cpu and memory resources for containers can be changed through the resize subresource`);
	}
	const a = allocationOf(pod);
	const containers = { ...a.containers };
	for (const c of body.spec.containers) {
		if (!(c.name in containers)) throw new Error(`The Pod "${name}" is invalid: spec.containers: Not found: "${c.name}"`);
		const cpu = c.resources?.requests?.cpu ?? c.resources?.limits?.cpu;
		if (cpu !== undefined) {
			if (Number.isNaN(millicores(cpu)) || millicores(cpu) <= 0) throw new Error(`The Pod "${name}" is invalid: spec.containers[0].resources.requests[cpu]: Invalid value: "${cpu}": must be greater than 0`);
			containers[c.name] = millicores(cpu);
		}
	}
	a.containers = containers;
	a.desired = Object.values(containers).reduce((s, v) => s + v, 0);
	await reconcile(cluster, pod);
	notify();
	return `pod/${name} patched`;
}

// reconcile tries to give a pod the CPU it asks for, like the kubelet does on
// every sync. If the node is full, the resize is Deferred, and the scheduler
// looks for lower-priority pods on the same node to preempt.
async function reconcile(cluster, pod) {
	const a = allocationOf(pod);
	if (a.desired === a.allocated) {
		a.pending = undefined;
		return;
	}
	const pods = (await cluster.api.corev1.listPodForAllNamespaces()).items;
	const used = usedOnNode(pods, pod);
	if (a.desired <= a.allocated || used + a.desired <= NODE_CPU) {
		a.allocated = a.desired;
		a.pending = undefined;
		a.inProgressUntil = Date.now() + IN_PROGRESS_MS;
		return;
	}
	if (a.desired > NODE_CPU) {
		a.pending = { reason: "Infeasible", message: `Node didn't have enough capacity: cpu, requested: ${a.desired}, capacity: ${NODE_CPU}` };
		return;
	}
	a.pending = { reason: "Deferred", message: `Node didn't have enough resource: cpu, requested: ${a.desired}, used: ${used}, capacity: ${NODE_CPU}` };

	// Preemption: evict the lowest-priority pods on the node until the resize
	// fits. Pods with the same or a higher priority are never touched. While
	// earlier victims are still shutting down, wait for them.
	if (pods.some((p) => p.metadata.deletionTimestamp && (a.preempted ?? []).includes(p.metadata.name))) return;
	const priority = priorityOf(pod);
	const candidates = pods
		.filter((p) => p.spec?.nodeName === pod.spec?.nodeName && p.metadata.uid !== pod.metadata.uid && !p.metadata.deletionTimestamp && priorityOf(p) < priority && allocationOf(p).allocated > 0)
		.sort((x, y) => priorityOf(x) - priorityOf(y));
	let free = NODE_CPU - used;
	const victims = [];
	for (const p of candidates) {
		if (free >= a.desired) break;
		victims.push(p);
		free += allocationOf(p).allocated;
	}
	if (free < a.desired) return;
	for (const victim of victims) {
		a.preempted = [...(a.preempted ?? []), victim.metadata.name];
		await cluster.api.corev1.deleteNamespacedPod({ namespace: victim.metadata.namespace, name: victim.metadata.name }).catch(() => {});
	}
}

export function preemptedBy(pod) {
	return allocationOf(pod).preempted ?? [];
}

export function startResizer(cluster) {
	controller ??= setInterval(async () => {
		const pending = [...allocations.entries()].filter(([, a]) => a.desired !== a.allocated);
		if (!pending.length) return;
		for (const [k] of pending) {
			const [namespace, name] = k.split("/");
			try {
				const pod = await cluster.api.corev1.readNamespacedPod({ namespace, name });
				await reconcile(cluster, pod);
			} catch {
				allocations.delete(k);
			}
		}
		notify();
	}, RECONCILE_MS);
}

export function deleteResizeNamespace(namespace) {
	for (const k of allocations.keys()) {
		if (k.startsWith(`${namespace}/`)) allocations.delete(k);
	}
	notify();
}
