// HorizontalPodAutoscaler for the simulated cluster, including scale to zero
// (HPAScaleToZero, beta and on by default since v1.37). webernetes has no
// autoscaling API, so HPAs live in a small store here and a controller loop
// scales their Deployments, like kube-controller-manager does.
//
// There is no metrics server, but there is a job queue per namespace and an
// external metrics adapter that serves its length as `pending_jobs`. Queue
// workers (patricklaabs/queue-worker) take jobs from the queue of their own
// namespace.

const KIND = "autoscaling/v2/HorizontalPodAutoscaler";
export const QUEUE_METRIC = "pending_jobs";

// A real HPA syncs every 15 seconds. The simulator is impatient.
const SYNC_MS = 2000;
const DEFAULT_SCALE_DOWN_WINDOW_SECONDS = 300;

const hpas = new Map();
const queues = new Map();
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

// Queue -------------------------------------------------------------------------

export function queueOf(namespace) {
	if (!queues.has(namespace)) queues.set(namespace, { pending: 0, done: 0, next: 1 });
	return queues.get(namespace);
}

export function addJobs(namespace, count) {
	queueOf(namespace).pending += count;
	notify();
}

// takeJob hands the next job to a worker, or undefined if the queue is empty.
export function takeJob(namespace) {
	const queue = queueOf(namespace);
	if (queue.pending === 0) return undefined;
	queue.pending--;
	return queue.next++;
}

export function returnJob(namespace) {
	queueOf(namespace).pending++;
	notify();
}

export function finishJob(namespace) {
	queueOf(namespace).done++;
	notify();
}

// HPA objects ---------------------------------------------------------------------

export function isAutoscalingKind(obj) {
	if (obj.kind !== "HorizontalPodAutoscaler") return false;
	if (`${obj.apiVersion}/${obj.kind}` === KIND) return true;
	throw new Error(`resource mapping not found for name: "${obj.metadata?.name}" namespace: "${obj.metadata?.namespace ?? ""}" from "STDIN": no matches for kind "${obj.kind}" in version "${obj.apiVersion}"\nensure CRDs are installed first`);
}

// validate mirrors the API server's validation for the fields the posts use.
function validate(obj) {
	const name = obj.metadata.name;
	const invalid = (field, value, message) => new Error(`The HorizontalPodAutoscaler "${name}" is invalid: ${field}: Invalid value: ${JSON.stringify(value)}: ${message}`);
	const spec = obj.spec ?? {};
	if (!spec.scaleTargetRef?.name || !spec.scaleTargetRef?.kind) throw new Error(`The HorizontalPodAutoscaler "${name}" is invalid: spec.scaleTargetRef: Required value`);
	if (!Number.isInteger(spec.maxReplicas) || spec.maxReplicas < 1) throw invalid("spec.maxReplicas", spec.maxReplicas, "must be greater than or equal to 1");
	const min = spec.minReplicas ?? 1;
	if (!Number.isInteger(min) || min < 0) throw invalid("spec.minReplicas", min, "must be greater than or equal to 0");
	if (min > spec.maxReplicas) throw invalid("spec.maxReplicas", spec.maxReplicas, "must be greater than or equal to `minReplicas`");
	const metrics = spec.metrics ?? [];
	if (min === 0 && !metrics.some((m) => m.type === "Object" || m.type === "External")) {
		throw invalid("spec.minReplicas", 0, "must specify at least one Object or External metric to support scaling to zero replicas");
	}
}

export function applyHPA(obj) {
	validate(obj);
	const k = key(obj.metadata.namespace, obj.metadata.name);
	const existing = hpas.get(k);
	const next = structuredClone(obj);
	next.metadata.creationTimestamp = existing?.metadata.creationTimestamp ?? new Date().toISOString();
	next.status = existing?.status ?? { currentReplicas: undefined, desiredReplicas: undefined, currentMetrics: [], conditions: [] };
	next.recommendations = existing?.recommendations ?? [];
	hpas.set(k, next);
	notify();
	return existing ? "configured" : "created";
}

export function listHPAs(namespace) {
	return [...hpas.values()].filter((h) => namespace === undefined || h.metadata.namespace === namespace);
}

export function deleteAutoscalingNamespace(namespace) {
	for (const [k, hpa] of hpas) {
		if (hpa.metadata.namespace === namespace) hpas.delete(k);
	}
	queues.delete(namespace);
	notify();
}

// Controller ----------------------------------------------------------------------

export function startAutoscaler(cluster) {
	controller ??= setInterval(() => {
		for (const hpa of hpas.values()) sync(cluster, hpa).catch((err) => console.error(err));
	}, SYNC_MS);
}

function setCondition(hpa, type, status, reason, message) {
	const conditions = hpa.status.conditions.filter((c) => c.type !== type);
	conditions.push({ type, status, reason, message });
	hpa.status.conditions = conditions;
}

export function conditionOf(hpa, type) {
	return hpa.status.conditions.find((c) => c.type === type);
}

// readMetric returns the value for one metric source, or an error message.
function readMetric(hpa, metric) {
	if (metric.type === "External" || metric.type === "Object") {
		const source = metric.type === "External" ? metric.external : metric.object;
		const name = source?.metric?.name;
		if (name !== QUEUE_METRIC) return { error: `unable to get external metric ${hpa.metadata.namespace}/${name}: no metrics returned from external metrics API` };
		return { name, value: queueOf(hpa.metadata.namespace).pending, target: source.target ?? {} };
	}
	if (metric.type === "Resource" || metric.type === "ContainerResource" || metric.type === "Pods") {
		const name = metric.resource?.name ?? metric.containerResource?.name ?? metric.pods?.metric?.name;
		return { error: `unable to get metrics for resource ${name}: no metrics returned from resource metrics API` };
	}
	return { error: `unknown metric source type "${metric.type}"` };
}

// desiredFor applies the HPA formula: desired = ceil(current * value / target).
// For AverageValue targets the current replica count cancels out, which is
// what lets an External metric wake a workload at zero replicas.
function desiredFor(reading, current) {
	const { value, target } = reading;
	if (target.type === "AverageValue") return Math.ceil(value / Number(target.averageValue));
	if (target.type === "Value") {
		if (current === 0) return value > 0 ? 1 : 0;
		return Math.ceil((current * value) / Number(target.value));
	}
	return current;
}

async function sync(cluster, hpa) {
	const { namespace } = hpa.metadata;
	const ref = hpa.spec.scaleTargetRef;
	let deployment;
	try {
		if (ref.kind !== "Deployment") throw new Error();
		deployment = await cluster.api.appsv1.readNamespacedDeployment({ namespace, name: ref.name });
	} catch {
		setCondition(hpa, "AbleToScale", "False", "FailedGetScale", `deployments/scale.apps "${ref.name}" not found`);
		notify();
		return;
	}
	setCondition(hpa, "AbleToScale", "True", "SucceededGetScale", "the HPA controller was able to get the target's current scale");

	const current = deployment.spec.replicas ?? 1;
	const min = hpa.spec.minReplicas ?? 1;
	const max = hpa.spec.maxReplicas;
	const readings = (hpa.spec.metrics ?? []).map((m) => readMetric(hpa, m));
	const good = readings.filter((r) => !r.error);
	hpa.status.currentMetrics = good.map((r) => ({ name: r.name, value: r.value, target: r.target }));
	hpa.status.currentReplicas = current;

	if (!good.length) {
		const error = readings.find((r) => r.error)?.error ?? "no metrics configured";
		setCondition(hpa, "ScalingActive", "False", "FailedGetMetrics", `the HPA was unable to compute the replica count: ${error}`);
		notify();
		return;
	}
	setCondition(hpa, "ScalingActive", "True", "ValidMetricFound", "the HPA was able to successfully calculate a replica count");

	const recommendation = Math.min(max, Math.max(min, ...good.map((r) => desiredFor(r, current))));

	// Scale-down stabilization: use the highest recommendation of the last
	// window, so a short dip doesn't remove pods that are needed again soon.
	// A new HPA starts with the current size as its first recommendation, like
	// kube-controller-manager does.
	const now = Date.now();
	if (!hpa.recommendations.length) hpa.recommendations.push({ at: now, replicas: current });
	const window = (hpa.spec.behavior?.scaleDown?.stabilizationWindowSeconds ?? DEFAULT_SCALE_DOWN_WINDOW_SECONDS) * 1000;
	hpa.recommendations = [...hpa.recommendations.filter((r) => now - r.at < window), { at: now, replicas: recommendation }];
	const desired = recommendation < current ? Math.max(recommendation, ...hpa.recommendations.map((r) => Math.min(r.replicas, current))) : recommendation;
	hpa.status.desiredReplicas = desired;

	if (desired !== current) {
		deployment.spec.replicas = desired;
		await cluster.api.appsv1.replaceNamespacedDeployment({ namespace, name: ref.name, body: deployment });
		hpa.status.currentReplicas = desired;
		hpa.status.lastScaleTime = new Date().toISOString();
	}
	if (desired === 0) setCondition(hpa, "ScaledToZero", "True", "ScaledToZero", "the HPA scaled the target to zero replicas");
	else setCondition(hpa, "ScaledToZero", "False", "NotScaledToZero", "the target has at least one replica");
	notify();
}
