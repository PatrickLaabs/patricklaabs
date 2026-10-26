# Autoscaling with Prometheus metrics, in practice

The HPA can't read Prometheus on its own. This post shows step by step how to connect the two on a real cluster, with complete YAML for requests per second, queue length and scale to zero, using prometheus-adapter or KEDA.

## The problem: the HPA doesn't speak PromQL

In the [Kubernetes v1.37 post](/posts/kubernetes-v1-37-live) we scaled a queue worker down to zero pods and back up again. The **HorizontalPodAutoscaler** (HPA), the Kubernetes controller that adjusts the number of pods to the load, watched the number of waiting jobs in a queue. In the simulator, that number simply existed. At the end we said that on a real cluster you need an adapter like prometheus-adapter or KEDA to provide it, but we didn't show how.

That gap is what this post fills. Many clusters already run **Prometheus**, a monitoring system that regularly collects numbers from your apps and stores them as time series. It knows how many requests your API handles and how many messages are waiting in your queue. These are often much better signals for scaling than CPU usage:

* An API that mostly waits for its database uses little CPU, even when it's overloaded. Requests per second show the load directly.
* A queue worker should scale with the amount of work that is *waiting*, not with how busy the workers that already exist are. And with zero pods, there's no CPU to measure at all.

The catch: the HPA can't query Prometheus. It only reads metrics from three fixed Kubernetes APIs:

| API | What it provides | Who usually serves it |
| --- | --- | --- |
| `metrics.k8s.io` | CPU and memory of pods and nodes | metrics-server |
| `custom.metrics.k8s.io` | Numbers that belong to a Kubernetes object, e.g. requests per second *of a pod* | prometheus-adapter |
| `external.metrics.k8s.io` | Numbers from outside the cluster's objects, e.g. the length of a queue | prometheus-adapter or KEDA |

Kubernetes itself doesn't implement the last two. It only defines what they look like. Something has to sit between Prometheus and the HPA, take the HPA's question ("what's the value of `http_requests_per_second` for the pods in namespace `shop`?"), turn it into a Prometheus query and send back the answer. That something is called a **metrics adapter**.

The whole chain looks like this:

1. Your app exposes a number on an HTTP endpoint, usually `/metrics`.
2. Prometheus collects it every few seconds. This is called **scraping**.
3. The adapter translates the HPA's questions into Prometheus queries.
4. The HPA compares the answer with your target value and adds or removes pods.

If any link is missing, the HPA shows `<unknown>` instead of a value and stops scaling. That's the most common problem in practice, so we'll go through each link and check it before moving on.

## What we'll do

We use two typical workloads:

* `shop-api`, a web API in the namespace `shop`. It should scale by **requests per second**.
* `image-resizer`, a queue worker in the namespace `images`. It takes jobs from the RabbitMQ queue `image-resize` and should scale by **queue length**, down to zero.

The steps:

1. Get the API's metric into Prometheus.
2. Write and test the Prometheus query.
3. Install prometheus-adapter and teach it the metric.
4. Scale the API by requests per second.
5. Scale the worker by queue length, down to zero, and see live what happens when the metric name is wrong.
6. Do the same with KEDA instead of prometheus-adapter.
7. Decide which of the two you need, and avoid the common pitfalls.

This post is about a real cluster, so most blocks are plain YAML that you apply with `kubectl` or Helm. We assume Prometheus was installed with the [kube-prometheus-stack](https://github.com/prometheus-community/helm-charts/tree/main/charts/kube-prometheus-stack) Helm chart, release name `kube-prometheus-stack`, in the namespace `monitoring`. If your setup differs, you mainly need to adjust the Prometheus address and one label. Step 5 has one live block that runs on the simulator in your browser, as in the earlier posts.

## Step 1: Get the metric into Prometheus

Before anything can scale on a number, Prometheus has to collect it. Our `shop-api` already exposes a **counter** called `http_requests_total` on port 8080 under `/metrics`. A counter is a number that only ever goes up: every request adds one. On its own it doesn't say much ("1,384,021 requests since the pod started"), but from its growth over time Prometheus can calculate the requests per second. You'll see that in step 2.

Most web frameworks have a Prometheus library that provides such a counter for you. You can check it with `curl localhost:8080/metrics` inside the pod. You should see a line like this:

```text
http_requests_total{method="GET",code="200"} 1384021
```

Next, Prometheus has to know where to find the endpoint. With kube-prometheus-stack, you tell it with a **ServiceMonitor**. That's a small object that says "scrape all pods behind the Services with these labels". Create it in the namespace of the app:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: shop-api
  namespace: shop
  labels:
    release: kube-prometheus-stack
spec:
  selector:
    matchLabels:
      app: shop-api
  endpoints:
    - port: http
      path: /metrics
      interval: 15s
```

What the fields mean:

* `selector` picks the Service of `shop-api` by its label `app: shop-api`.
* `port: http` is the *name* of the port in that Service, not the number. If your Service port has no name, give it one.
* `interval: 15s` means Prometheus fetches the numbers every 15 seconds.
* The label `release: kube-prometheus-stack` is easy to forget. By default, the Prometheus from kube-prometheus-stack only picks up ServiceMonitors with this label. Without it, the ServiceMonitor exists, but nothing happens, and there's no error message anywhere.

**How to check it:** open the Prometheus UI with a port forward:

```sh
kubectl port-forward -n monitoring svc/kube-prometheus-stack-prometheus 9090
```

Then open `http://localhost:9090/targets` in your browser. After about half a minute you should see an entry `serviceMonitor/shop/shop-api/0` with one line per pod and the state `UP`. If it's missing, check the `release` label first.

Prometheus automatically attaches labels to every number it collects this way, among them `namespace` and `pod`. We'll need those two in step 3.

## Step 2: Write and test the query

Before the HPA uses a query, you should look at its result yourself. Open the **Graph** tab in the Prometheus UI and enter:

```promql
sum by (pod) (rate(http_requests_total{namespace="shop"}[2m]))
```

Read it from the inside out:

* `http_requests_total{namespace="shop"}` selects the counters of all pods in the namespace `shop`.
* `rate(…[2m])` turns each counter into "requests per second, averaged over the last two minutes".
* `sum by (pod) (…)` adds up the different `method` and `code` combinations, so you get exactly one number per pod.

You should see one line per pod. Send some traffic to the API, and the lines should go up within a minute.

A note on the `[2m]`: `rate` needs at least two measurements in the window, better a few more. A rule of thumb is four times the scrape interval. With 15 seconds, one minute is the minimum, two minutes is on the safe side. A longer window makes the value calmer but also slower to react.

## Step 3: Install prometheus-adapter and teach it the metric

[prometheus-adapter](https://github.com/kubernetes-sigs/prometheus-adapter) is the metrics adapter from the Kubernetes project. It serves `custom.metrics.k8s.io` and `external.metrics.k8s.io` and answers every question with a Prometheus query. Which metrics it offers, and under which names, is set by **rules** in its configuration.

Save this as `adapter-values.yaml`:

```yaml
prometheus:
  url: http://kube-prometheus-stack-prometheus.monitoring.svc
  port: 9090

rules:
  default: false
  custom:
    - seriesQuery: 'http_requests_total{namespace!="",pod!=""}'
      resources:
        overrides:
          namespace: {resource: "namespace"}
          pod: {resource: "pod"}
      name:
        matches: "^(.*)_total$"
        as: "${1}_per_second"
      metricsQuery: 'sum(rate(<<.Series>>{<<.LabelMatchers>>}[2m])) by (<<.GroupBy>>)'
```

What the parts do:

* `prometheus.url` and `port` say where Prometheus is. This is the Service that kube-prometheus-stack creates.
* `default: false` switches off the chart's built-in rules. They would offer hundreds of metrics that nobody scales on. With only your own rules, the adapter stays fast and its list of metrics stays readable.
* `seriesQuery` says which Prometheus series this rule is about: `http_requests_total`, but only where the labels `namespace` and `pod` are set.
* `resources.overrides` links Prometheus labels to Kubernetes objects: the label `namespace` is a Namespace, the label `pod` is a Pod. That's what makes it a metric *of a pod*, which the HPA can ask for.
* `name` sets the name the HPA will use. The regular expression cuts off `_total` and adds `_per_second`, so the HPA asks for `http_requests_per_second`. A counter's total isn't useful for scaling, a rate is, and the name should say so.
* `metricsQuery` is the query from step 2, written as a template. When the HPA asks, the adapter fills in `<<.Series>>` (the metric name), `<<.LabelMatchers>>` (the namespace and the pods of the Deployment) and `<<.GroupBy>>` (here: `pod`).

Install the adapter with Helm into the same namespace as Prometheus:

```sh
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm install prometheus-adapter prometheus-community/prometheus-adapter \
  -n monitoring -f adapter-values.yaml
```

**How to check it:** ask the API directly, just like the HPA will. The `*` means "all pods in the namespace":

```sh
kubectl get --raw "/apis/custom.metrics.k8s.io/v1beta1/namespaces/shop/pods/*/http_requests_per_second"
```

The answer is JSON with one entry per pod. The interesting part looks like this:

```json
{
  "describedObject": { "kind": "Pod", "name": "shop-api-7c9f8b6d4-x2lq8" },
  "metric": { "name": "http_requests_per_second" },
  "value": "4200m"
}
```

Don't be surprised by `4200m`. Kubernetes writes numbers as **quantities**, the same format as CPU requests. The `m` stands for "milli", so `4200m` is 4.2 requests per second.

If you get `NotFound` instead, the adapter didn't find any matching series. Check the `seriesQuery` in the Prometheus UI, and look at the adapter's log with `kubectl logs -n monitoring deploy/prometheus-adapter`.

## Step 4: Scale the API by requests per second

Now the HPA. We use the metric type `Pods`. It means: "ask for this metric for every pod of the Deployment and take the average". With `averageValue: "50"`, the HPA aims for 50 requests per second per pod.

The HPA calculates the number of pods like this: *current pods × current value ÷ target value*, rounded up. Three pods at an average of 80 requests per second give 3 × 80 ÷ 50 = 4.8, so five pods. If the load drops to 20 per pod, it's 3 × 20 ÷ 50 = 1.2, so two pods.

```yaml
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: shop-api
  namespace: shop
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: shop-api
  minReplicas: 2
  maxReplicas: 10
  metrics:
    - type: Pods
      pods:
        metric:
          name: http_requests_per_second
        target:
          type: AverageValue
          averageValue: "50"
  behavior:
    scaleUp:
      policies:
        - type: Percent
          value: 100
          periodSeconds: 60
    scaleDown:
      stabilizationWindowSeconds: 300
```

The choices behind the numbers:

* **Where does 50 come from?** From a load test. Find out how many requests per second one pod handles before its response times get worse, and take about 70% of that as the target. The remaining 30% is headroom while new pods are still starting.
* **`minReplicas: 2`**: an API that customers use shouldn't depend on a single pod. If it crashes or its node is restarted, the second one keeps serving.
* **`scaleUp` policy**: the HPA may at most double the number of pods per minute (100% per 60 seconds). That's fast enough for real load and protects you from a measuring error scaling you straight to `maxReplicas`.
* **`scaleDown` window**: the HPA uses the highest recommendation of the last five minutes before it removes pods. Traffic often comes in waves, and this keeps pods from being removed in a short dip and started again right after.
* As in the v1.37 post: **remove `replicas` from the Deployment**, otherwise every `kubectl apply` resets the number of pods.

Apply the HPA and check it:

```sh
kubectl get hpa -n shop
```

```text
NAME       REFERENCE             TARGETS     MINPODS   MAXPODS   REPLICAS   AGE
shop-api   Deployment/shop-api   4200m/50    2         10        2          1m
```

`TARGETS` shows the current average next to the target. As soon as it's a number and not `<unknown>`, the whole chain works.

**Tip:** you can list more than one metric, for example requests per second *and* CPU. The HPA calculates a number of pods for each and takes the highest. That way, whichever limit is reached first wins.

## Step 5: Scale a worker by queue length, down to zero

Now the worker. Its signal is the number of messages waiting in the RabbitMQ queue `image-resize`. RabbitMQ's built-in Prometheus plugin reports this as `rabbitmq_queue_messages_ready`, with a label `queue` for the queue name, once per-queue metrics are switched on (`prometheus.return_per_object_metrics = true`). Other systems have similar metrics, for example the consumer lag of Kafka.

This number doesn't belong to a pod of the worker. It comes from RabbitMQ, which runs in its own namespace. That's exactly what **external metrics** are for. And as you saw in the v1.37 post, an external metric is also the condition for `minReplicas: 0`: it still exists when no worker is running.

Add an `external` section to the `rules` in your `adapter-values.yaml`, below `custom` and at the same indentation:

```yaml
  external:
    - seriesQuery: 'rabbitmq_queue_messages_ready{queue!=""}'
      resources:
        namespaced: false
      name:
        matches: "^rabbitmq_queue_messages_ready$"
        as: "pending_jobs"
      metricsQuery: 'sum(<<.Series>>{<<.LabelMatchers>>}) by (queue)'
```

Two things are different from the custom rule:

* `namespaced: false`: normally the adapter only looks for series whose `namespace` label matches the namespace of the HPA. Our series comes from RabbitMQ, so its `namespace` label is the one RabbitMQ runs in, not `images`. This setting turns that filter off.
* `as: "pending_jobs"` gives the metric a short, neutral name. The HPA doesn't need to know that RabbitMQ is behind it. If you switch to a different queue system later, only the rule changes.

Update the adapter with `helm upgrade prometheus-adapter prometheus-community/prometheus-adapter -n monitoring -f adapter-values.yaml` and check the new metric. The `labelSelector` at the end picks our queue (`%3D` is an encoded `=`):

```sh
kubectl get --raw "/apis/external.metrics.k8s.io/v1beta1/namespaces/images/pending_jobs?labelSelector=queue%3Dimage-resize"
```

Then the HPA. It's very close to the one from the v1.37 post. New is the `selector`: the adapter passes its labels on to the query, so the HPA only sees the queue `image-resize`, even if RabbitMQ has many queues.

```yaml
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: image-resizer
  namespace: images
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: image-resizer
  minReplicas: 0
  maxReplicas: 10
  metrics:
    - type: External
      external:
        metric:
          name: pending_jobs
          selector:
            matchLabels:
              queue: image-resize
        target:
          type: AverageValue
          averageValue: "5"
  behavior:
    scaleDown:
      stabilizationWindowSeconds: 120
```

`averageValue: "5"` means at most five waiting messages per pod: with 30 messages the HPA wants six pods, with zero messages it wants none. The two-minute scale-down window keeps the last worker around for a moment after the queue has been emptied, in case the next batch is about to arrive.

### Try it: what the HPA sees when the name is wrong

The most common mistake with this setup is the metric name. The HPA has to use the name the *adapter* offers, not the name in Prometheus. Our rule renamed `rabbitmq_queue_messages_ready` to `pending_jobs`, so the Prometheus name doesn't exist for the HPA anymore.

The simulator has a small built-in adapter that serves the length of a job queue as `pending_jobs`, just like our rule. The worker image takes one job from the queue every two seconds. The block below makes the mistake on purpose: the HPA asks for the Prometheus name. (The simulator ignores `selector`, so we leave it out here.)

1. Apply the block. The Deployment starts with one pod, and the HPA reports `ScalingActive: False` with the message that no metrics were returned from the external metrics API. Notice that the pod keeps running: if the HPA can't read its metric, it doesn't scale at all, neither up nor down.
2. Open the terminal with `~` and run `kubectl get hpa -n live-1`. The `TARGETS` column shows `<unknown>`, just like on a real cluster with this mistake.
3. Change `rabbitmq_queue_messages_ready` in the block to `pending_jobs` and click **▶ kubectl apply** again. The error disappears, and after about ten seconds the HPA scales the idle worker to zero.
4. Click **+10 jobs**. The HPA starts two workers, and once the queue is empty they go back to zero.

```k8s-live
apiVersion: apps/v1
kind: Deployment
metadata:
  name: image-resizer
spec:
  selector:
    matchLabels:
      app: image-resizer
  template:
    metadata:
      labels:
        app: image-resizer
    spec:
      containers:
        - name: worker
          image: patricklaabs/queue-worker:1.0
---
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: image-resizer
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: image-resizer
  minReplicas: 0
  maxReplicas: 5
  metrics:
    - type: External
      external:
        metric:
          name: rabbitmq_queue_messages_ready
        target:
          type: AverageValue
          averageValue: "5"
  behavior:
    scaleDown:
      stabilizationWindowSeconds: 10
```

On a real cluster, you find the full error message with `kubectl describe hpa -n images image-resizer`, in the `Conditions` and `Events` at the end. To see which external metrics the adapter offers at all, run `kubectl get --raw /apis/external.metrics.k8s.io/v1beta1`.

## Step 6: The same with KEDA

[KEDA](https://keda.sh/) (Kubernetes Event-driven Autoscaling) takes a different approach. Instead of writing adapter rules and an HPA, you write a single object, a **ScaledObject**. In it you say which Deployment to scale and which Prometheus query to use. KEDA then creates the HPA for you, serves the external metric itself and sends your query to Prometheus directly. There's no rule that renames anything, so the naming mistake from step 5 can't happen.

Install KEDA with Helm:

```sh
helm repo add kedacore https://kedacore.github.io/charts
helm install keda kedacore/keda -n keda --create-namespace
```

Then the ScaledObject for our worker. If you followed step 5, first delete the HPA `image-resizer`, because KEDA refuses to take over a Deployment that another HPA already scales.

```yaml
apiVersion: keda.sh/v1alpha1
kind: ScaledObject
metadata:
  name: image-resizer
  namespace: images
spec:
  scaleTargetRef:
    name: image-resizer
  minReplicaCount: 0
  maxReplicaCount: 10
  pollingInterval: 15
  cooldownPeriod: 120
  triggers:
    - type: prometheus
      metadata:
        serverAddress: http://kube-prometheus-stack-prometheus.monitoring.svc:9090
        query: sum(rabbitmq_queue_messages_ready{queue="image-resize"})
        threshold: "5"
  fallback:
    failureThreshold: 3
    replicas: 2
```

How it maps to what you already know:

* `minReplicaCount` and `maxReplicaCount` are the HPA's `minReplicas` and `maxReplicas`.
* `query` is a normal PromQL query. It must return exactly one number. That's why it uses `sum(…)`, even though there's only one queue.
* `threshold: "5"` is the target per pod, like `averageValue: "5"` in the HPA.
* `pollingInterval: 15` means KEDA asks Prometheus every 15 seconds whether there's work.
* `cooldownPeriod: 120` is how long the queue has to stay empty before KEDA goes from one pod to zero.
* `fallback` is something the plain HPA doesn't have: if the query fails three times in a row, for example because Prometheus is down, KEDA scales to two pods. Your worker keeps working, even though nobody can measure how much work there is.

An important difference: KEDA does the step between zero and one pod itself. The HPA it creates only handles one pod and more. That's why scale to zero with KEDA also works on clusters older than v1.37. KEDA can also read many sources without Prometheus, among them RabbitMQ, Kafka, SQS and cron schedules.

**How to check it:** `kubectl get scaledobject -n images` should show `READY True` and `ACTIVE False` while the queue is empty. KEDA's HPA is called `keda-hpa-image-resizer` and shows up in `kubectl get hpa -n images`, like any other HPA.

## Step 7: Which one, and what to watch out for

**prometheus-adapter or KEDA?** Both work, and both end up with a normal HPA.

* Choose **prometheus-adapter** if you mainly scale on metrics of your pods, such as requests per second, and want plain HPA objects. It's one small component, and every HPA looks like the ones in the Kubernetes documentation.
* Choose **KEDA** if you scale on queues and other systems, need scale to zero on clusters older than v1.37, or want a `fallback`. You write a PromQL query instead of adapter rules.

**Only one of them can serve external metrics.** Each of the three metrics APIs is registered in the cluster with an **APIService** object, and there can only be one per API. KEDA always registers `external.metrics.k8s.io`. The prometheus-adapter chart does the same as soon as you define `external` rules. If you install both that way, they fight over the same object: either the second installation fails, or one takes the API over and the HPAs of the other one show `<unknown>`. Combining them works like this: KEDA serves external metrics, and prometheus-adapter only has `custom` rules. You can see who currently serves the API with:

```sh
kubectl get apiservice v1beta1.external.metrics.k8s.io
```

**If Prometheus is down, the HPA stands still.** It can't calculate anything and keeps the current number of pods. That's a sensible default, but at zero pods it means the worker won't start, however full the queue gets. With prometheus-adapter, alert on it: an HPA whose `ScalingActive` condition is `False` should page someone. With KEDA, the `fallback` covers it.

**The chain has a delay.** Scraping (15 seconds), the `rate` window, the adapter and the HPA's own sync interval (15 seconds) add up. Expect the HPA to react about 30 to 60 seconds after the load changes, plus the time a new pod needs to start. Sudden spikes have to be covered by the pods that are already running. That's what the headroom in your target value and `minReplicas` are for.

## Summary

* The HPA can't query Prometheus. It reads `custom.metrics.k8s.io` and `external.metrics.k8s.io`, and a **metrics adapter** answers those questions with Prometheus queries.
* Check every link of the chain on its own: the target in the Prometheus UI, the query in the Graph tab, the metric with `kubectl get --raw`, and finally `TARGETS` in `kubectl get hpa`.
* Use **`Pods` metrics** for values of your own pods, like requests per second, and **`External` metrics** for values from outside, like queue length. Only external (or object) metrics allow `minReplicas: 0`.
* The HPA uses the name the **adapter** offers, not the Prometheus name. A wrong name shows up as `<unknown>`, and then the HPA doesn't scale at all.
* **KEDA** combines adapter and HPA in one ScaledObject with a plain PromQL query, handles scale to zero itself and has a `fallback` for when Prometheus is unreachable.
* Only one component can serve `external.metrics.k8s.io`. If you use KEDA and prometheus-adapter together, give external metrics to KEDA.
