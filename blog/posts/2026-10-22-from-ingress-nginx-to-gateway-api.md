# From ingress-nginx to Gateway API, live

ingress-nginx, the most widely used way to route web traffic into Kubernetes, gets no more security patches. This post shows step by step, live in your browser, how to move a small shop to its successor, Gateway API, and which pitfalls to watch for.

## Why this matters

When someone opens your website, the request first lands on an **Ingress controller**. That's a proxy in your cluster that looks at the hostname and path of every request and forwards it to the right app. You configure it with **Ingress** objects: "requests for `shop.example.com/api` go to the API, everything else goes to the storefront".

For years, the most popular Ingress controller has been ingress-nginx. In [November 2025](https://kubernetes.io/blog/2025/11/11/ingress-nginx-retirement/) the Kubernetes networking group (SIG Network) announced its retirement. Maintenance ended in March 2026, and since then there have been no new releases, no bug fixes and no security patches.

The tricky part: nothing broke. Existing installations simply keep running. In many clusters, the component that receives every single request from the internet is now unmaintained, and nobody noticed.

So what are the options? The Ingress *API* itself stays, only this controller is gone. You could switch to a different Ingress controller, but ingress-nginx is configured with lots of special `nginx.ingress.kubernetes.io/*` annotations, and other controllers don't understand them. On top of that, the Ingress API won't get any new features. If you have to rewrite your routing configuration anyway, it makes sense to rewrite it for its successor right away: [Gateway API](https://gateway-api.sigs.k8s.io/).

## What we'll do

We migrate a small shop. It consists of two apps behind one hostname, `shop.example.com`: a storefront (the website) and an API under `/api`. We go through it in five steps:

1. Look at the current setup with ingress-nginx, including a hidden behavior that's easy to overlook.
2. Learn the three building blocks of Gateway API.
3. Translate the setup and find the bug that a typical migration introduces.
4. Try a canary release, something that is much easier with Gateway API.
5. Understand the most common error after a migration: a route that doesn't attach.

At the end there's a plan for migrating a real cluster without downtime.

As in the [probes post](/posts/readiness-vs-liveness-probes), every manifest runs on a simulated cluster in your browser. Click **▶ kubectl apply**, then send requests with the **curl** bar below the block. The buttons under the bar are ready-made requests, and you can also type your own. The simulator brings two proxies: one that behaves like ingress-nginx, and a Gateway API implementation called `webernetes`.

## Step 1: The starting point, an Ingress with annotations

This is what many real Ingress configurations look like. One detail matters: the API expects its requests at `/users`, not at `/api/users`. The `/api` prefix only exists so the proxy knows where to send the request, so it has to be removed before the request reaches the API. With ingress-nginx you do that with two annotations: `use-regex` allows a regular expression as the path, and `rewrite-target: /$2` replaces the path with the part after `/api/`.

The apps are plain Pods with a Service each, to keep the manifests short. Apply the block and click all three requests.

```k8s-live
# curl shop.example.com/
# curl shop.example.com/api/users
# curl shop.example.com/API/users
apiVersion: v1
kind: Pod
metadata:
  name: storefront
  labels:
    app: storefront
spec:
  containers:
    - name: web
      image: patricklaabs/storefront:1.0
---
apiVersion: v1
kind: Service
metadata:
  name: storefront
spec:
  selector:
    app: storefront
  ports:
    - port: 80
      targetPort: 8080
---
apiVersion: v1
kind: Pod
metadata:
  name: shop-api
  labels:
    app: shop-api
spec:
  containers:
    - name: api
      image: patricklaabs/shop-api:1.0
---
apiVersion: v1
kind: Service
metadata:
  name: shop-api
spec:
  selector:
    app: shop-api
  ports:
    - port: 80
      targetPort: 8080
---
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: shop-api
  annotations:
    nginx.ingress.kubernetes.io/use-regex: "true"
    nginx.ingress.kubernetes.io/rewrite-target: /$2
spec:
  ingressClassName: nginx
  rules:
    - host: shop.example.com
      http:
        paths:
          - path: /api(/|$)(.*)
            pathType: ImplementationSpecific
            backend:
              service:
                name: shop-api
                port:
                  number: 80
---
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: storefront
spec:
  ingressClassName: nginx
  rules:
    - host: shop.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: storefront
                port:
                  number: 80
```

All three requests work. The third one deserves a second look: `/API/users` in capital letters also reaches the API. Why?

ingress-nginx generates one nginx configuration block per hostname. As soon as *one* Ingress for that hostname uses a regex or `rewrite-target`, nginx treats *every* path of that hostname as a regex that ignores upper and lower case. That even applies to the `storefront` Ingress, which has no annotations at all.

Nobody wrote this behavior down on purpose, but your users might rely on it. Exactly this kind of behavior gets lost silently in a migration.

## Step 2: The three building blocks of Gateway API

An Ingress does everything in one object. Gateway API splits the same job into three objects. The split follows who is responsible for what in a typical company:

* **GatewayClass** names the implementation, meaning the software that actually forwards the requests. The cluster operator installs it. Examples are Envoy Gateway, Cilium, Istio and kgateway. In our simulator it's called `webernetes`.
* **Gateway** is the entry point: which ports, protocols and hostnames are accepted. It's usually managed by the platform team.
* **HTTPRoute** contains the routing rules for one app and is managed by the team that owns the app. With `parentRefs` it says which Gateway it belongs to.

Another important difference: path rewrites, header matches and traffic splits are no longer annotations with free-form text. They are real fields in the spec. Kubernetes can therefore check them when you apply them, and they mean the same thing in every implementation.

You don't have to write the first draft by hand. The tool [ingress2gateway](https://github.com/kubernetes-sigs/ingress2gateway), which reached version 1.0 in March 2026, reads your Ingresses directly from the cluster and translates them:

```sh
ingress2gateway print --providers=ingress-nginx -A
```

It translates the common annotations and warns about everything it can't translate. Take those warnings seriously, because one of them affects our shop: `rewrite-target` with capture groups like `$2` is not translated.

## Step 3: Translate the shop and find the bug

Here is the shop on Gateway API, the way it looks when someone skipped that warning. Instead of two Ingresses there is now one Gateway and one HTTPRoute. The route has two rules: paths starting with `/api` go to the API, everything else goes to the storefront.

Apply the block and send the three requests again.

```k8s-live
# curl shop.example.com/
# curl shop.example.com/api/users
# curl shop.example.com/API/users
apiVersion: v1
kind: Pod
metadata:
  name: storefront
  labels:
    app: storefront
spec:
  containers:
    - name: web
      image: patricklaabs/storefront:1.0
---
apiVersion: v1
kind: Service
metadata:
  name: storefront
spec:
  selector:
    app: storefront
  ports:
    - port: 80
      targetPort: 8080
---
apiVersion: v1
kind: Pod
metadata:
  name: shop-api
  labels:
    app: shop-api
spec:
  containers:
    - name: api
      image: patricklaabs/shop-api:1.0
---
apiVersion: v1
kind: Service
metadata:
  name: shop-api
spec:
  selector:
    app: shop-api
  ports:
    - port: 80
      targetPort: 8080
---
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: shop
spec:
  gatewayClassName: webernetes
  listeners:
    - name: http
      protocol: HTTP
      port: 80
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: shop
spec:
  parentRefs:
    - name: shop
  hostnames:
    - shop.example.com
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /api
      backendRefs:
        - name: shop-api
          port: 80
    - backendRefs:
        - name: storefront
          port: 80
```

`/api/users` now ends in a `404`. The error message tells you why: the API receives `/api/users`, because nothing removes the `/api` prefix anymore.

To fix it, add a `URLRewrite` filter to the first rule, directly above `backendRefs` and at the same indentation, then apply again:

```yaml
      filters:
        - type: URLRewrite
          urlRewrite:
            path:
              type: ReplacePrefixMatch
              replacePrefixMatch: /
```

`ReplacePrefixMatch` replaces exactly the part of the path that the `PathPrefix` rule matched, here `/api`, with `/`. No regular expression, no capture groups, and anyone reading it understands what happens. In the Gateway API spec, `URLRewrite` has "Extended" support, which means not every implementation has to offer it. So check that yours does. The big ones do.

And `/API/users`? It now lands on the storefront, because paths in Gateway API are case-sensitive. That's intended by the spec, and most of the time it's what you want. But if some client out there calls your API in capital letters, you'd only find out after the switch. So search your access logs for such paths *before* you migrate.

## Step 4: Canary releases without a second Ingress

With a **canary release** you send only a small part of the traffic to a new version first. If it works, you increase the share step by step. If not, only a few users are affected.

With ingress-nginx you need a second Ingress with the same host and path plus the annotations `canary: "true"` and `canary-weight`. With Gateway API, you simply set weights on the `backendRefs` of one route. The route below sends 90% of the requests to version 1 and 10% to version 2. In addition, testers who send the header `x-canary: always` always get version 2.

Apply the block and click the first request a few times. Most answers come from v1, now and then one from v2. The second request always lands on v2.

```k8s-live
# curl shop.example.com/
# curl -H "x-canary: always" shop.example.com/
apiVersion: v1
kind: Pod
metadata:
  name: storefront-v1
  labels:
    app: storefront
    version: v1
spec:
  containers:
    - name: web
      image: patricklaabs/storefront:1.0
---
apiVersion: v1
kind: Service
metadata:
  name: storefront-v1
spec:
  selector:
    app: storefront
    version: v1
  ports:
    - port: 80
      targetPort: 8080
---
apiVersion: v1
kind: Pod
metadata:
  name: storefront-v2
  labels:
    app: storefront
    version: v2
spec:
  containers:
    - name: web
      image: patricklaabs/storefront:2.0
---
apiVersion: v1
kind: Service
metadata:
  name: storefront-v2
spec:
  selector:
    app: storefront
    version: v2
  ports:
    - port: 80
      targetPort: 8080
---
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: shop
spec:
  gatewayClassName: webernetes
  listeners:
    - name: http
      protocol: HTTP
      port: 80
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: storefront
spec:
  parentRefs:
    - name: shop
  hostnames:
    - shop.example.com
  rules:
    - matches:
        - headers:
            - name: x-canary
              value: always
      backendRefs:
        - name: storefront-v2
          port: 80
    - backendRefs:
        - name: storefront-v1
          port: 80
          weight: 90
        - name: storefront-v2
          port: 80
          weight: 10
```

Change the weights to `50` and `50` and apply again: the split changes immediately. A rollout becomes a series of small changes to a single object, each easy to review. That fits well with GitOps, where every change goes through a pull request.

## Step 5: When the route just won't attach

The most common question after a migration: "I applied the HTTPRoute, so why does everything return 404?" Usually the route didn't attach to the Gateway, and `kubectl apply` doesn't warn you about it. The YAML is valid, so it gets accepted. Whether the Gateway actually uses the route only shows up later, in the route's status.

In this example the platform team has restricted the Gateway to hostnames matching `*.example.com`. The route, however, wants to serve `example.com`. Apply the block, look at the route's status line below the block and try both requests.

```k8s-live
# curl example.com/
# curl www.example.com/
apiVersion: v1
kind: Pod
metadata:
  name: storefront
  labels:
    app: storefront
spec:
  containers:
    - name: web
      image: patricklaabs/storefront:1.0
---
apiVersion: v1
kind: Service
metadata:
  name: storefront
spec:
  selector:
    app: storefront
  ports:
    - port: 80
      targetPort: 8080
---
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: shop
spec:
  gatewayClassName: webernetes
  listeners:
    - name: http
      protocol: HTTP
      port: 80
      hostname: "*.example.com"
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: landing
spec:
  parentRefs:
    - name: shop
  hostnames:
    - example.com
  rules:
    - backendRefs:
        - name: storefront
          port: 80
```

The status shows `Accepted False (NoMatchingListenerHostname)`. The wildcard `*.example.com` covers `www.example.com`, but not the bare domain `example.com` itself. There are two ways to fix it: add a second listener with `hostname: example.com` to the Gateway, or change the route to `www.example.com`.

On a real cluster, `kubectl describe httproute <name>` shows the same information under `status.parents`. Make checking it a fixed part of every migration. In the simulator's terminal (`~`), `kubectl get gateways` and `kubectl get httproutes -n live-4` work too.

## A migration plan without a maintenance window

This is how you can move a real cluster step by step, without downtime:

1. **Take inventory.** Find out which annotations you actually use:
   `kubectl get ingress -A -o yaml | grep -o 'nginx.ingress.kubernetes.io/[a-z-]*' | sort | uniq -c`
   Pay the most attention to `configuration-snippet` and `server-snippet`. They have no equivalent in Gateway API. They also let anyone who can create an Ingress inject raw nginx configuration, which has led to several security vulnerabilities over the years. [IngressNightmare](https://kubernetes.io/blog/2025/03/24/ingress-nginx-cve-2025-1974/) in 2025 also smuggled configuration in through annotations. You won't miss them.
2. **Translate.** Create a first draft with `ingress2gateway print`, then work through every warning by hand, especially regex paths and rewrites.
3. **Run both side by side.** The Gateway gets its own IP address, while your Services and Pods stay exactly as they are. Test the new path before you change DNS, for example with `curl --resolve shop.example.com:443:<gateway-ip> https://shop.example.com/...`. That sends the request to the Gateway even though DNS still points to the old controller.
4. **Switch over gradually.** Lower the DNS TTL (how long clients cache the address) first, then switch DNS to the Gateway and watch the error rate. Keep ingress-nginx running for a while so you can switch back if needed.
5. **Clean up.** Only then delete the Ingresses and ingress-nginx itself.

## Summary

* **Unmaintained isn't the same as broken.** ingress-nginx keeps running, and that's exactly why it gets forgotten. Put the migration on your roadmap before the next security vulnerability forces you to.
* **Annotations are behavior.** Every annotation is a feature that needs a replacement or a conscious decision to drop it. Look for hidden behavior too, like regex paths that ignore upper and lower case.
* **Check the `status`, not just `kubectl apply`.** An HTTPRoute that wasn't accepted is valid YAML that does nothing.
* **Use the split of responsibilities.** The platform team manages Gateways, app teams manage their HTTPRoutes, and nobody needs access to the controller's configuration anymore.
