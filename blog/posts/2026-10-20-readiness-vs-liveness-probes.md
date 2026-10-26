# Readiness vs. liveness probes, live

Kubernetes can tell whether your container is running, but not whether your app actually works. Probes fill that gap. In this post you see, step by step and live in your browser, which probe to use when and what goes wrong without them.

## The problem: running is not the same as working

Kubernetes starts your container and sees that the process is running. Whether your app inside it can actually answer requests, Kubernetes doesn't know. Maybe it's still loading data, maybe it's stuck. From the outside, all of these look the same: the process is there.

**Probes** close that gap. A probe is a small check, usually an HTTP request to an address in your app like `/healthz`, that the **kubelet** (the Kubernetes agent on every node that starts and watches containers) runs at regular intervals. Depending on the answer, Kubernetes reacts. The two most common probes ask two very different questions:

* **Readiness probe:** "Can this pod handle requests right now?" If the answer is no, Kubernetes stops sending it traffic. Nothing is restarted. As soon as the probe succeeds again, the pod gets traffic again.
* **Liveness probe:** "Is this container broken beyond repair?" If the answer is no, the kubelet kills the container and starts a fresh one.

Mixing them up is one of the classic ways to take down a perfectly healthy app. That's why we'll look at each case on its own below.

As in the [last post](/posts/kubernetes-live-in-your-browser), every manifest runs on a simulated cluster in your browser. Click **▶ kubectl apply** to start it, change the text and apply again to try variations. Press `~` to open a terminal for `kubectl` commands. Each block runs in its own namespace, which is shown in its header.

## Step 1: Without a readiness probe, users get errors

Our example app `patricklaabs/slowstart` is a small shop. After starting, it needs about eight seconds to load its data (to warm its cache). Until then it answers every request with the error `503 Service Unavailable`.

The manifest below starts two copies of the shop (a **Deployment** with two replicas) and a **Service** that distributes requests across them. There is no readiness probe. Kubernetes therefore considers a pod ready as soon as its process starts, and the Service sends it requests right away.

Apply the block and click the **curl shop** button a few times right after the pods start.

```k8s-live
apiVersion: apps/v1
kind: Deployment
metadata:
  name: shop
spec:
  replicas: 2
  selector:
    matchLabels:
      app: shop
  template:
    metadata:
      labels:
        app: shop
    spec:
      containers:
        - name: shop
          image: patricklaabs/slowstart:1.0
          ports:
            - containerPort: 8080
---
apiVersion: v1
kind: Service
metadata:
  name: shop
spec:
  type: NodePort
  selector:
    app: shop
  ports:
    - port: 80
      targetPort: 8080
```

For the first few seconds you get `503`. That's what your users would see too. On a real cluster this doesn't only happen once: it happens on every rollout, every time you scale up and every time a node is drained for maintenance, because new pods are always starting somewhere.

## Step 2: With a readiness probe, pods wait until they're ready

Now we tell Kubernetes how to check whether the shop is ready. The app offers the address `/readyz` for that. It answers `ok` as soon as the cache is warm. The new `readinessProbe` block lets the kubelet ask this address every second (`periodSeconds: 1`). A pod only receives requests from the Service once the check succeeds.

Apply the block and watch the `READY` column. It shows `0/1` for a few seconds, and only then switches to `1/1`.

```k8s-live
apiVersion: apps/v1
kind: Deployment
metadata:
  name: shop
spec:
  replicas: 2
  selector:
    matchLabels:
      app: shop
  template:
    metadata:
      labels:
        app: shop
    spec:
      containers:
        - name: shop
          image: patricklaabs/slowstart:1.0
          ports:
            - containerPort: 8080
          readinessProbe:
            httpGet:
              path: /readyz
              port: 8080
            periodSeconds: 1
---
apiVersion: v1
kind: Service
metadata:
  name: shop
spec:
  type: NodePort
  selector:
    app: shop
  ports:
    - port: 80
      targetPort: 8080
```

If you click curl during the first seconds, you now get `no ready endpoints` instead of a `503`. That means the Service doesn't know a single pod it may send requests to. That looks worse at first, but it's the right behavior: in this demo all pods start at the same time. On a real cluster, new pods start while the old ones are still running. Because the new ones only get traffic once they're ready, requests keep going to the old, working pods in the meantime. Your users don't notice anything.

This blog uses exactly that trick, by the way. When Kubernetes is about to stop a pod, it first sends it the signal `SIGTERM`. The blog then reports itself as not ready (`503` on its readiness address), so the Service stops sending it requests before it shuts down. That's why the [Chaos Monkey](/#chaos) on the front page can kill pods without visitors seeing errors.

## Step 3: A liveness probe restarts a stuck app

Now for the other probe. Our second example app, `patricklaabs/deadlock`, works fine for about fifteen seconds. Then it gets stuck: it waits for a lock that is never released (a so-called deadlock). The process doesn't crash, it just stops working and answers every request with `500`.

Without a liveness probe, Kubernetes wouldn't notice. The pod would show `Running` forever while serving nothing but errors. The `livenessProbe` below asks the address `/healthz` every two seconds. After three failed checks in a row (`failureThreshold: 3`), the kubelet restarts the container.

Apply the block, click **curl stuck** now and then, and keep an eye on the `RESTARTS` column. After each restart the app works again for fifteen seconds, until the next deadlock.

```k8s-live
apiVersion: v1
kind: Pod
metadata:
  name: stuck
  labels:
    app: stuck
spec:
  containers:
    - name: app
      image: patricklaabs/deadlock:1.0
      ports:
        - containerPort: 8080
      livenessProbe:
        httpGet:
          path: /healthz
          port: 8080
        periodSeconds: 2
        failureThreshold: 3
---
apiVersion: v1
kind: Service
metadata:
  name: stuck
spec:
  type: NodePort
  selector:
    app: stuck
  ports:
    - port: 80
      targetPort: 8080
```

For comparison, delete the `livenessProbe` block and apply again. The pod stays at `Running` and `1/1`, looks perfectly healthy, and still only answers with errors.

A restart is not a real fix, of course. The bug in the app still has to be fixed. But until then, the liveness probe keeps the app usable most of the time.

## Step 4: Find the bug yourself

Someone added a liveness probe to the slow-starting shop from step 1, "to be safe". Apply the block and watch the restart counter. Then press `~` and run `kubectl logs -n live-4 too-eager` to see what the app was doing when it was killed.

```k8s-live
apiVersion: v1
kind: Pod
metadata:
  name: too-eager
spec:
  containers:
    - name: shop
      image: patricklaabs/slowstart:1.0
      ports:
        - containerPort: 8080
      livenessProbe:
        httpGet:
          path: /healthz
          port: 8080
        periodSeconds: 2
        failureThreshold: 3
```

The logs show the app warming up and answering the probe with `503`, but never the line `Cache warm, ready to serve traffic`. Here's what happens: the app needs eight seconds to start. The probe checks every two seconds and gives up after three failures, so after about six seconds. The kubelet kills the container just before it would have been ready. The new container starts loading from the beginning, gets killed again, and so on. In the end the pod lands in `CrashLoopBackOff`. A healthy app, restarted to death.

The fix is a third probe, the **startup probe**. As long as it hasn't succeeded, Kubernetes pauses the liveness and readiness probes. That gives slow starts their own, more generous time budget. Add this block below the `livenessProbe` (at the same indentation) and apply again:

```yaml
      startupProbe:
        httpGet:
          path: /healthz
          port: 8080
        periodSeconds: 1
        failureThreshold: 30
```

That allows the app up to 30 seconds (30 attempts, one per second) for its first successful check. After that, the strict liveness probe takes over.

## Summary

* **Readiness** decides whether a pod gets traffic. Use it for every container that answers requests.
* **Liveness** decides whether a container is restarted. Only use it for failures a restart actually fixes, like a deadlock. Never let it check a database or another service: if that dependency goes down, every pod restarts at the same time, and one outage turns into two.
* **Startup** protects slow starts. Use it instead of a very large `initialDelaySeconds`.
* Keep the check addresses cheap. A probe runs every few seconds on every pod, for as long as the pod exists.
