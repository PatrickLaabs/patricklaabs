# Readiness vs. liveness probes, live

Kubernetes knows whether your container is *running*. It has no idea whether your app is actually *working*. Probes close that gap, and the two most common ones answer two very different questions:

* **Readiness:** "Should this pod get traffic right now?" If not, it is taken out of the Service, but nothing gets restarted.
* **Liveness:** "Is this container broken beyond repair?" If so, the kubelet kills it and starts a fresh one.

Mixing them up is one of the classic ways to take down a perfectly healthy app. As in the [last post](#kubernetes-live-in-your-browser), every manifest below runs on a simulated cluster in your browser: hit **▶ kubectl apply**, edit, apply again.

## No readiness probe: traffic too early

`patricklaabs/slowstart` needs about eight seconds to warm its cache before it can serve real requests. Until then it answers `503`. There is no readiness probe below, so Kubernetes marks the pod `Ready` as soon as the process starts and the Service happily sends it traffic. Apply it and hammer the **curl** button.

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

Every user who lands on a pod during warm-up gets an error. On a real cluster this happens on every rollout, every scale-up and every node drain.

## With a readiness probe: wait until it's warm

Same app, one extra block. The kubelet now asks `/readyz` every second, and the pod only joins the Service once it says `ok`. Watch the `READY` column stay at `0/1` for a few seconds while curl finds no endpoints at all, and then every request succeeds.

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

"No endpoints" sounds worse than a `503`, but with more than one replica it means traffic goes to the pods that *are* ready. That is exactly what keeps the [Chaos Monkey](#chaos) on the front page from causing errors: this blog flips its own readiness to `503` when it receives `SIGTERM`, so the Service stops sending it requests before it shuts down.

## Liveness: when the process lives but the app is dead

`patricklaabs/deadlock` serves fine for about fifteen seconds, then gets stuck waiting for a lock that is never released. The process doesn't crash, so without a probe Kubernetes would keep it around forever, answering `500` to everyone. The liveness probe below notices after three failed checks and has the kubelet restart the container. Keep an eye on the `RESTARTS` column: each restart buys another fifteen good seconds, until the next deadlock.

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

Delete the `livenessProbe` block and apply again to see the alternative: a pod that says `Running` and `1/1` while serving nothing but errors.

## Spot the bug

Someone added a liveness probe to the slow-starting app "to be safe". Apply it and watch the restart counter. Then press `~` and run `kubectl describe pod -n <namespace>` to see why.

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

The app needs eight seconds to warm up, but the probe gives up after three failures two seconds apart. The kubelet kills the container just before it would have been ready, it starts warming up again, gets killed again, and ends up in `CrashLoopBackOff`. A healthy app, restarted to death.

The fix is a **startup probe**. While it runs, liveness and readiness checks are on hold, so slow boots get their own, more generous budget. Add this next to the liveness probe and apply again:

```yaml
      startupProbe:
        httpGet:
          path: /healthz
          port: 8080
        periodSeconds: 1
        failureThreshold: 30
```

That allows up to 30 seconds for the first successful check, after which the strict liveness probe takes over.

## Rules of thumb

* **Readiness** decides about traffic. Use it on everything that serves requests.
* **Liveness** decides about restarts. Only use it for failures a restart actually fixes, like a deadlock. Never let it check a database or another service: if that dependency goes down, every pod restarts at once and you have turned one outage into two.
* **Startup** protects slow boots. Reach for it instead of a huge `initialDelaySeconds`.
* Keep the endpoints cheap. A probe runs every few seconds on every pod, forever.
