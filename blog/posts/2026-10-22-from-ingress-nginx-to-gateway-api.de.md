# Von ingress-nginx zur Gateway API, live

ingress-nginx ist in Rente. SIG Network hat das im [November 2025](https://kubernetes.io/blog/2025/11/11/ingress-nginx-retirement/) angekündigt, die Wartung nach bestem Bemühen endete im März 2026, und seitdem gibt es keine Releases, keine Bugfixes und keine Sicherheitsupdates mehr. Das Tückische daran: Kaputt gegangen ist nichts. Bestehende Installationen laufen einfach weiter, und so ist in vielen Clustern die exponierteste Komponente, die jede Anfrage aus dem Internet annimmt, unbemerkt ungewartet.

Die Ingress-*API* verschwindet nicht, nur dieser Controller. Du könntest auf einen anderen Ingress-Controller wechseln, aber deine `nginx.ingress.kubernetes.io/*`-Annotationen kommen nicht mit, und die Ingress-API ist ohnehin eingefroren. Wenn du deine Routing-Konfiguration sowieso neu schreibst, dann gleich für die [Gateway API](https://gateway-api.sigs.k8s.io/).

Dieser Beitrag geht die Migration an einem kleinen Shop durch: ein Storefront und eine API hinter einem Hostnamen. Wie im [Probes-Beitrag](/de/posts/readiness-vs-liveness-probes) läuft jedes Manifest auf einem simulierten Cluster in deinem Browser. Klick auf **▶ kubectl apply** und schick dann über die **curl**-Leiste Anfragen. Der Simulator bringt zwei Edge-Proxies mit: einen, der sich wie ingress-nginx verhält, und eine Gateway-API-Implementierung mit der GatewayClass `webernetes`.

## Der Ausgangspunkt: ein Ingress mit Annotationen

So sehen viele echte Ingresses aus. Die API erwartet Anfragen unter `/users`, nicht unter `/api/users`, also schneiden ein Regex-Pfad und `rewrite-target` das Präfix ab. Die Apps sind einfache Pods, damit die Manifeste kurz bleiben.

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

Alle drei Anfragen funktionieren, und die dritte lohnt einen zweiten Blick: Auch `/API/users` in Großbuchstaben landet bei der API. ingress-nginx erzeugt pro Host einen nginx-`server`-Block, und sobald *ein* Ingress für einen Host Regex oder `rewrite-target` nutzt, wird jeder Pfad dieses Hosts zu einer Regex, die Groß- und Kleinschreibung ignoriert. Sogar beim `storefront`-Ingress, der gar keine Annotationen hat. Genau solches Verhalten geht bei einer Migration still verloren.

## Dieselbe App, drei Objekte

Die Gateway API teilt auf, was der Ingress allein erledigt hat, und zwar entlang der Frage, wem in einer echten Organisation was gehört:

* **GatewayClass** ist die Implementierung, installiert vom Cluster-Betrieb. Envoy Gateway, Cilium, Istio, kgateway und andere bringen jeweils ihre eigene mit. Hier heißt sie `webernetes`.
* **Gateway** ist der Eingang: welche Ports, Protokolle und Hostnamen angenommen werden. Gehört meist dem Plattform-Team.
* **HTTPRoute** ist das Routing für eine App und gehört dem App-Team. Sie hängt sich über `parentRefs` an ein Gateway.

Pfad-Rewrites, Header-Matches und Traffic-Splitting sind keine Annotationen mit String-Werten mehr, sondern typisierte Felder in der Spec. Sie werden also validiert und bedeuten für jede Implementierung dasselbe.

Den ersten Entwurf musst du nicht von Hand schreiben. [ingress2gateway](https://github.com/kubernetes-sigs/ingress2gateway), seit März 2026 in Version 1.0, liest deine Ingresses direkt aus dem Cluster:

```sh
ingress2gateway print --providers=ingress-nginx -A
```

Es übersetzt die gängigen Annotationen und warnt bei allem, was es nicht kann. Nimm diese Warnungen ernst, denn eine davon betrifft unseren Shop: `rewrite-target` mit Capture-Groups wie `$2` wird nicht übersetzt.

## Finde den Fehler

Hier ist der Shop auf der Gateway API, so übersetzt, wie eine Migration mit einer übersprungenen Warnung endet. Wende ihn an und probier die drei Anfragen noch einmal.

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

`/api/users` endet jetzt mit einem `404`, und die Fehlermeldung verrät warum: Die API bekommt `/api/users`, weil niemand mehr das Präfix abschneidet. Ergänze in der ersten Regel neben `backendRefs` einen `URLRewrite`-Filter und wende das Manifest erneut an:

```yaml
      filters:
        - type: URLRewrite
          urlRewrite:
            path:
              type: ReplacePrefixMatch
              replacePrefixMatch: /
```

`ReplacePrefixMatch` ersetzt genau den Teil, auf den `PathPrefix` gepasst hat. Keine Regex, keine Capture-Groups, und man kann lesen, was passiert. `URLRewrite` hat in der Spec den Support-Level „Extended“, prüf also, ob deine Implementierung es kann. Die großen können es.

Und `/API/users`? Landet beim Storefront, denn Pfade sind in der Gateway API case-sensitive. Das ist so gewollt und meistens auch richtig. Aber wenn irgendwo da draußen ein Client deine API in Großbuchstaben aufruft, merkst du das erst nach der Umstellung. Also: *vor* der Migration in den Access-Logs nach solchen Pfaden suchen.

## Canary-Releases ohne zweiten Ingress

Mit ingress-nginx braucht ein Canary einen zweiten Ingress mit gleichem Host und Pfad plus die Annotationen `canary: "true"` und `canary-weight`. Mit der Gateway API sind es einfach Gewichte an den `backendRefs` einer einzigen Route. Die Route unten schickt 10 % des Traffics an v2, und Tester, die den Header `x-canary: always` mitschicken, bekommen immer v2. Klick ein paar Mal auf die erste curl-Vorlage.

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

Ändere die Gewichte auf `50` und `50`, wende an, und die Verteilung ändert sich sofort. Ein Rollout wird so zu einer Reihe kleiner, reviewbarer Diffs an einem Objekt, was gut zu GitOps passt.

## Wenn die Route einfach nicht greift

Die häufigste Frage nach einer Migration: „Ich habe die HTTPRoute angewendet, warum kommt überall 404?“ Meistens hat sich die Route nicht an das Gateway gehängt, und `kubectl apply` beschwert sich darüber nicht. Hier hat das Plattform-Team das Gateway auf `*.example.com` beschränkt. Wende das Manifest an und schau auf die Statuszeile der Route.

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

`Accepted False (NoMatchingListenerHostname)`: Die Wildcard `*.example.com` deckt `www.example.com` ab, aber nicht die Apex-Domain `example.com` selbst. Ergänze einen zweiten Listener mit `hostname: example.com` oder ändere die Route auf `www.example.com`. Auf einem echten Cluster zeigt `kubectl describe httproute` dieselben Conditions unter `status.parents`. Mach den Blick darauf zum festen Teil jeder Migration. Das Terminal (`~`) kennt auch `kubectl get gateways` und `kubectl get httproutes -n <namespace>`.

## Ein Migrationsplan ohne Wartungsfenster

1. **Inventur machen.** `kubectl get ingress -A -o yaml | grep -o 'nginx.ingress.kubernetes.io/[a-z-]*' | sort | uniq -c` zeigt, welche Annotationen du wirklich nutzt. `configuration-snippet` und `server-snippet` haben kein Gegenstück und brauchen die meiste Aufmerksamkeit. Mit ihnen kann jeder, der einen Ingress anlegen darf, rohe nginx-Konfiguration einschleusen, was über die Jahre zu mehreren CVEs geführt hat. Auch [IngressNightmare](https://kubernetes.io/blog/2025/03/24/ingress-nginx-cve-2025-1974/) hat 2025 Konfiguration über Annotationen eingeschmuggelt. Vermissen wirst du sie nicht.
2. **Übersetzen.** `ingress2gateway print` für den ersten Entwurf, dann jede Warnung von Hand abarbeiten, vor allem Regex-Pfade und Rewrites.
3. **Beides parallel betreiben.** Das Gateway bekommt eine eigene IP-Adresse, Services und Pods bleiben unverändert. Teste mit `curl --resolve shop.example.com:443:<gateway-ip> https://shop.example.com/...`, bevor das DNS dorthin zeigt.
4. **Schrittweise umschalten.** DNS-TTL senken, umschalten, Fehlerrate beobachten. ingress-nginx noch eine Weile laufen lassen, damit du zurückschalten kannst.
5. **Aufräumen.** Erst dann die Ingresses und den Controller löschen.

## Faustregeln

* **Ungewartet ≠ kaputt.** ingress-nginx läuft weiter, und genau deshalb wird es vergessen. Setz die Migration auf die Roadmap, bevor die nächste CVE das für dich tut.
* **Annotationen sind Verhalten.** Jede Annotation ist ein Feature, das einen Ersatz oder eine bewusste Entscheidung braucht. Such auch nach den impliziten, etwa Regex-Pfaden ohne Beachtung der Groß- und Kleinschreibung.
* **Prüf den `status`, nicht nur `kubectl apply`.** Eine HTTPRoute, die nicht akzeptiert wurde, ist gültiges YAML, das nichts tut.
* **Nutz die Rollenaufteilung.** Gateways fürs Plattform-Team, HTTPRoutes für die App-Teams, und niemand braucht mehr Zugriff auf die ConfigMap des Controllers.
