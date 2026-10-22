# From ingress-nginx to Gateway API, live

ingress-nginx is retired. SIG Network announced it in [November 2025](https://kubernetes.io/blog/2025/11/11/ingress-nginx-retirement/), best-effort maintenance ended in March 2026, and since then there have been no releases, no bug fixes and no security patches. The tricky part: nothing broke. Existing installations keep running, so in many clusters the most exposed component, the one that terminates every request from the internet, is now unmaintained without anyone noticing.

The Ingress *API* isn't going anywhere, only this controller is. You could switch to another Ingress controller, but your `nginx.ingress.kubernetes.io/*` annotations won't come along, and the Ingress API is feature-frozen anyway. If you're rewriting your routing config, you might as well rewrite it for [Gateway API](https://gateway-api.sigs.k8s.io/).

This post walks through that migration with a small shop: a storefront and an API behind one hostname. As in the [probes post](/posts/readiness-vs-liveness-probes), every manifest runs on a simulated cluster in your browser. Hit **▶ kubectl apply**, then send requests with the **curl** bar. The simulator brings two edge proxies: one that behaves like ingress-nginx, and a Gateway API implementation with the GatewayClass `webernetes`.

## The starting point: an Ingress with annotations

This is what a lot of real Ingresses look like. The API expects requests at `/users`, not `/api/users`, so a regex path and `rewrite-target` strip the prefix. The apps are plain Pods to keep the manifests short.

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

All three requests work, and the third one is worth a second look: `/API/users` with capital letters also reaches the API. ingress-nginx renders one nginx `server` block per host, and as soon as *one* Ingress for a host uses regex or `rewrite-target`, every path of that host becomes a case-insensitive regex. Even the `storefront` Ingress, which has no annotations at all. Behavior like this is exactly what silently goes missing in a migration.

## Same app, three objects

Gateway API splits up what the Ingress did alone, along the lines of who owns what in a real organization:

* **GatewayClass** is the implementation, installed by the cluster operator. Envoy Gateway, Cilium, Istio, kgateway and others each bring their own. Here it's `webernetes`.
* **Gateway** is the entry point: which ports, protocols and hostnames are accepted. Usually owned by the platform team.
* **HTTPRoute** is the routing for one app, owned by the app team. It attaches itself to a Gateway via `parentRefs`.

Path rewrites, header matches and traffic splitting are no longer annotations with string values but typed fields in the spec. That means they get validated, and they mean the same thing for every implementation.

You don't have to write the first draft by hand. [ingress2gateway](https://github.com/kubernetes-sigs/ingress2gateway), which reached 1.0 in March 2026, reads your Ingresses straight from the cluster:

```sh
ingress2gateway print --providers=ingress-nginx -A
```

It translates the common annotations and warns about everything it can't. Take those warnings seriously, because one of them concerns our shop: `rewrite-target` with capture groups like `$2` is not translated.

## Spot the bug

Here's the shop on Gateway API, translated the way a migration with a skipped warning ends up. Apply it and try the three requests again.

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

`/api/users` now ends in a `404`, and the error message tells you why: the API receives `/api/users` because nothing strips the prefix anymore. Add a `URLRewrite` filter to the first rule, next to `backendRefs`, and apply again:

```yaml
      filters:
        - type: URLRewrite
          urlRewrite:
            path:
              type: ReplacePrefixMatch
              replacePrefixMatch: /
```

`ReplacePrefixMatch` replaces exactly the part matched by `PathPrefix`. No regex and no capture groups, and you can read what happens. `URLRewrite` has "Extended" support in the spec, so check that your implementation supports it. The big ones do.

And `/API/users`? It lands on the storefront, because Gateway API paths are case-sensitive. That's what the spec intends, and in most cases it's what you want. But if a client somewhere out there calls your API in capital letters, you'll only find out after the switch. So: check your access logs for such paths *before* migrating.

## Canary releases without a second Ingress

With ingress-nginx, a canary needs a second Ingress with the same host and path plus the annotations `canary: "true"` and `canary-weight`. With Gateway API it's just weights on the `backendRefs` of a single route. The route below sends 10% of the traffic to v2, and testers who send the header `x-canary: always` always get v2. Click the first curl preset a few times.

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

Change the weights to `50` and `50`, apply, and the split changes immediately. A rollout is now a series of small, reviewable diffs to one object, which fits GitOps nicely.

## When the route just won't attach

The most common question after a migration: "I applied the HTTPRoute, why does everything return 404?" Usually the route didn't attach to the Gateway, and Kubernetes doesn't complain about it in `kubectl apply`. Here the platform team has restricted the Gateway to `*.example.com`. Apply and look at the route's status line.

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

`Accepted False (NoMatchingListenerHostname)`: the wildcard `*.example.com` covers `www.example.com`, but not the apex domain `example.com` itself. Add a second listener with `hostname: example.com`, or change the route to `www.example.com`. On a real cluster, `kubectl describe httproute` shows the same conditions under `status.parents`. Make checking them part of every migration. The terminal (`~`) knows `kubectl get gateways` and `kubectl get httproutes -n <namespace>` too.

## A migration plan that doesn't need a maintenance window

1. **Take inventory.** `kubectl get ingress -A -o yaml | grep -o 'nginx.ingress.kubernetes.io/[a-z-]*' | sort | uniq -c` shows which annotations you really use. `configuration-snippet` and `server-snippet` have no equivalent and need the most attention. They let anyone who can create an Ingress inject raw nginx config, which has led to several CVEs over the years. [IngressNightmare](https://kubernetes.io/blog/2025/03/24/ingress-nginx-cve-2025-1974/) in 2025 also smuggled config in through annotations. You won't miss them.
2. **Translate.** `ingress2gateway print` for the first draft, then work through every warning by hand, especially regex paths and rewrites.
3. **Run both side by side.** The Gateway gets its own IP address, and Services and Pods stay unchanged. Test with `curl --resolve shop.example.com:443:<gateway-ip> https://shop.example.com/...` before DNS points there.
4. **Switch over gradually.** Lower the DNS TTL, switch, watch the error rate. Leave ingress-nginx running for a while so you can switch back.
5. **Clean up.** Only then delete the Ingresses and the controller.

## Rules of thumb

* **Unmaintained ≠ broken.** ingress-nginx keeps running, and that's exactly why it gets forgotten. Put the migration on your roadmap before the next CVE does it for you.
* **Annotations are behavior.** Every annotation is a feature that needs a replacement or a conscious decision. Look for the implicit ones too, like case-insensitive regex paths.
* **Check `status`, not just `kubectl apply`.** An HTTPRoute that wasn't accepted is valid YAML that does nothing.
* **Use the role split.** Gateways for the platform team, HTTPRoutes for the app teams, and nobody needs access to the controller's ConfigMap anymore.
