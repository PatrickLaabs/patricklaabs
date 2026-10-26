# Kubernetes v1.37, live

Kubernetes v1.37 can shut down idle workloads completely and give running pods more CPU without restarting them. In this post you try both, step by step and live in your browser, and learn when each one helps.

## What's new and what we'll try

[Kubernetes v1.37](https://kubernetes.io/blog/2026/08/26/kubernetes-v1-37-release/) came out in August 2026. Many of its changes are about the same goal: using CPU and memory more efficiently. We'll try the two that you notice most in day-to-day work:

1. **Scale to zero with the HorizontalPodAutoscaler** (beta, on by default). An app that has nothing to do can now be scaled down to zero pods, and comes back automatically when there's work.
2. **Scheduler preemption for in-place pod resize** (alpha). Giving a running pod more CPU without a restart has worked since v1.35. New in v1.37: if the node is full, Kubernetes can make room by removing less important pods.

At the end you'll find a short look at two more changes, pod-level resource managers and Memory QoS, which work inside the node and can't really be shown in a browser.

As in the [earlier posts](/posts/kubernetes-live-in-your-browser), every manifest below runs on a simulated cluster in your browser. Click **▶ kubectl apply** to start it, then watch the status below the block. Some blocks have ready-made `kubectl` commands below them that you can run with a click. Press `~` to open a terminal. Each block runs in its own namespace, which is shown in its header.

## Part 1: Scale to zero

### The problem: idle pods still cost money

Imagine a worker that processes jobs from a queue, for example resizing uploaded images. During the day there's a lot to do, at night nothing at all.

The **HorizontalPodAutoscaler** (HPA) adjusts the number of pods to the load. It regularly measures a metric, compares it with a target value and adds or removes pods. Until now, though, it could never go below one pod. So at least one worker kept running all night, waiting for jobs that never came. If you have many such workers, that adds up.

Since v1.37 you can set `minReplicas: 0`. There's one condition, and to understand it, it's worth trying the obvious approach first.

### Step 1: Why CPU doesn't work as a metric

The most common HPA setting scales by CPU usage: "if the pods use more than 50% of their CPU, add pods". Let's try that with `minReplicas: 0`. Apply the block and read the answer.

```k8s-live
apiVersion: apps/v1
kind: Deployment
metadata:
  name: worker
spec:
  selector:
    matchLabels:
      app: worker
  template:
    metadata:
      labels:
        app: worker
    spec:
      containers:
        - name: worker
          image: patricklaabs/queue-worker:1.0
---
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: worker
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: worker
  minReplicas: 0
  maxReplicas: 5
  metrics:
    - type: Resource
      resource:
        name: cpu
        target:
          type: Utilization
          averageUtilization: 50
```

The Deployment is created, but the API server rejects the HPA: `must specify at least one Object or External metric to support scaling to zero replicas`.

That makes sense once you think about it. CPU usage is measured on running pods. At zero pods there's nothing to measure, so the HPA would never find out that there's work again. It needs a signal that exists *outside* the pods. That's what the metric types `External` and `Object` are for, for example the length of a queue.

### Step 2: Scale a queue worker to zero and back

Now the same worker, but the HPA watches the number of waiting jobs in the queue (the external metric `pending_jobs`). The target `averageValue: "5"` means: there should be at most five waiting jobs per pod. With 10 waiting jobs the HPA wants two pods, with 25 it wants five, and with zero it wants none.

Two more details in the manifest:

* The Deployment has **no `replicas` field**. The HPA decides the number of pods. If the field were in the manifest, every new `kubectl apply` (or your GitOps tool) would reset the number and work against the HPA.
* `stabilizationWindowSeconds: 10` tells the HPA to wait ten seconds before removing pods, so a short break in the queue doesn't immediately shut everything down. The default is five minutes. We shorten it here so you don't have to wait so long.

Here's what to do:

1. Apply the block. The Deployment starts with one pod. After about ten seconds the HPA removes it, and the status line shows `ScaledToZero: True`.
2. Click **+10 jobs**. The HPA sees ten waiting jobs and starts two pods.
3. Watch the queue line: each worker processes one job every two seconds.
4. Once the queue is empty, the HPA waits ten seconds and scales back to zero.

Click **+10 jobs** several times quickly to see the HPA go up to five pods (`maxReplicas`).

```k8s-live
apiVersion: apps/v1
kind: Deployment
metadata:
  name: worker
spec:
  selector:
    matchLabels:
      app: worker
  template:
    metadata:
      labels:
        app: worker
    spec:
      containers:
        - name: worker
          image: patricklaabs/queue-worker:1.0
---
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: worker
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: worker
  minReplicas: 0
  maxReplicas: 5
  metrics:
    - type: External
      external:
        metric:
          name: pending_jobs
        target:
          type: AverageValue
          averageValue: "5"
  behavior:
    scaleDown:
      stabilizationWindowSeconds: 10
```

In the terminal, `kubectl get hpa -n live-2` shows the same numbers as on a real cluster: the current value of the metric, the target and the number of pods.

### What you need on a real cluster

The simulator comes with a built-in source for `pending_jobs`. On a real cluster, the HPA asks the **external metrics API** for the value, and something has to provide it: an adapter such as [prometheus-adapter](https://github.com/kubernetes-sigs/prometheus-adapter) or [KEDA](https://keda.sh/), which reads the value from your queue or monitoring system. Installing the HPA doesn't install an adapter.

Keep three things in mind before you scale a workload to zero:

* **The first job has to wait.** When the queue fills up again, the HPA has to notice it, and the pod has to be scheduled, pull its image and start. That can take seconds or minutes.
* **The metric must come from outside the app.** A metric that the worker itself reports disappears together with the last pod.
* **Stopping pods must not lose work.** A job should only count as done once it has really been processed. The workers in this demo put a job back into the queue if they are stopped in the middle of it.

## Part 2: More CPU without a restart

### Background: in-place pod resize

For a long time, changing the CPU or memory of a pod meant replacing it: delete the old pod, start a new one. Since v1.35, Kubernetes can resize a running pod **in place**. The container keeps running, the restart counter stays at zero, and the app doesn't even notice, apart from now having more CPU available.

You don't do this with a normal `kubectl apply`, but with the separate `resize` subresource:

```sh
kubectl patch pod shop --subresource resize -p '{"spec":{"containers":[{"name":"app","resources":{"requests":{"cpu":"1"}}}]}}'
```

The kubelet (the Kubernetes agent on the node) checks whether the node has enough free CPU. If so, it applies the new value right away.

### Step 3: Resize a running pod

The pod below requests half a CPU (`500m`, i.e. 500 millicores). With `nodeName` we place it on a fixed node. In this demo, every node has room for 2 CPUs.

1. Apply the block and wait until the pod is `Running`. The `CPU` column shows `500m`.
2. Click the `kubectl patch` command below the block. The pod briefly shows `PodResizeInProgress`, then the `CPU` column shows `1`, and `RESTARTS` is still `0`.
3. Now try the other way: change `cpu: 500m` in the block to `cpu: 750m` and click **▶ kubectl apply**. The API server rejects it. The resources of a running pod can only be changed through the `resize` subresource, not through a normal update.

```k8s-live
# kubectl patch pod shop --subresource resize -p '{"spec":{"containers":[{"name":"app","resources":{"requests":{"cpu":"1"}}}]}}'
apiVersion: v1
kind: Pod
metadata:
  name: shop
spec:
  nodeName: node-2
  containers:
    - name: app
      image: nginx:1.27
      resources:
        requests:
          cpu: 500m
```

### The problem: what if the node is full?

So far the node had enough room. But what happens if a pod wants more CPU than is still free on its node? Until v1.36, the kubelet marked the resize as **Deferred** (postponed) and waited. If nothing else on the node ever stopped, the pod waited forever.

New in v1.37 (alpha): the scheduler can now **preempt** other pods to make room. Preempting means it removes pods that are less important. Which pods are less important is decided by their **PriorityClass**, a number you give a pod. A higher number means more important. Only pods on the same node with a *lower* priority can be removed.

The feature is alpha and therefore switched off by default. On a real cluster you enable it with the feature gate `InPlacePodVerticalScalingSchedulerPreemption`. In the simulator it's on.

### Step 4: Make room through preemption

Two pods share one node with room for 2 CPUs:

* `shop` is our important web shop with priority `1000` and half a CPU.
* `report` is a nightly report job with priority `100` and 1.2 CPUs.

Together they use 1.7 CPUs. Now the shop wants 1.5 CPUs. That's 2.7 in total, more than the node has.

1. Apply the block and wait until both pods are `Running`.
2. Click the first command below the block. The shop asks for 1.5 CPUs. The resize is `Deferred` at first, because the node is full.
3. The scheduler sees that `report` has a lower priority and removes it (`preempted: report`). As soon as the report pod is gone, the shop gets its 1.5 CPUs, still without a restart.
4. Click the second command. Now the shop asks for 3 CPUs. The status shows `Infeasible`: the node has only 2 CPUs in total, so no amount of preemption helps. Kubernetes doesn't even try.

```k8s-live
# kubectl patch pod shop --subresource resize -p '{"spec":{"containers":[{"name":"app","resources":{"requests":{"cpu":"1500m"}}}]}}'
# kubectl patch pod shop --subresource resize -p '{"spec":{"containers":[{"name":"app","resources":{"requests":{"cpu":"3"}}}]}}'
apiVersion: scheduling.k8s.io/v1
kind: PriorityClass
metadata:
  name: shop
value: 1000
---
apiVersion: scheduling.k8s.io/v1
kind: PriorityClass
metadata:
  name: batch
value: 100
---
apiVersion: v1
kind: Pod
metadata:
  name: shop
spec:
  nodeName: node-3
  priorityClassName: shop
  containers:
    - name: app
      image: nginx:1.27
      resources:
        requests:
          cpu: 500m
---
apiVersion: v1
kind: Pod
metadata:
  name: report
spec:
  nodeName: node-3
  priorityClassName: batch
  containers:
    - name: app
      image: busybox:1.36
      command: ["sleep", "3600"]
      resources:
        requests:
          cpu: 1200m
```

For comparison: click **↺ reset**, change the `priorityClassName` of `report` to `shop` and apply again. Now both pods are equally important. Kubernetes won't remove a pod for another one with the same priority, so the resize stays `Deferred`.

Two things to keep in mind on a real cluster:

* The removed pod is gone. If it belongs to a Deployment, a new one is started, possibly on another node. A bare pod like our report job just ends. So only give a low priority to work that can be repeated.
* Preemption only looks at the pod's own node. Kubernetes doesn't move the resized pod to a bigger node, because then it wouldn't be "in place" anymore.

## Also new: work inside the node

Two more changes in v1.37 are about how the node itself hands out CPU and memory. You can't really watch them in a browser, but they're good to know:

* **Pod-level resource managers** (beta, off by default). Since v1.34 you can set a CPU and memory budget for the whole pod (`spec.resources`) instead of for each container. Now the kubelet's CPU, memory and topology managers can use that budget too, for example to give a latency-critical pod exclusive CPU cores close to its memory, without every small helper container in the pod needing its own dedicated cores.
* **Memory QoS** (beta). On Linux nodes with cgroup v2, the kubelet tells the kernel how much memory each container is guaranteed. The kernel then protects that memory from being taken away. If you also set the kubelet option `memoryThrottlingFactor`, the kernel starts reclaiming a container's memory a bit before it reaches its limit. That slows the container down a little, but makes it less likely that it is abruptly stopped with an out-of-memory error.

## Summary

* **Scale to zero:** with `minReplicas: 0`, the HPA can shut a workload down completely when there's nothing to do. It needs an `External` or `Object` metric, such as queue length, because CPU can't be measured on pods that don't exist.
* **Leave `replicas` out of the Deployment** when an HPA manages it, otherwise every apply resets the number of pods.
* **In-place resize** changes a running pod's CPU without a restart, through `kubectl patch --subresource resize`.
* **Preemption for resize** (alpha) makes room on a full node by removing pods with a lower priority. If the request is bigger than the whole node, the resize is `Infeasible`.
