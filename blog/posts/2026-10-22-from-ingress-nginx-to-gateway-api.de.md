# Von ingress-nginx zur Gateway API, live

ingress-nginx, der meistgenutzte Weg, Web-Traffic in Kubernetes zu routen, bekommt keine Sicherheitsupdates mehr. Dieser Post zeigt Schritt für Schritt und live im Browser, wie du einen kleinen Shop auf den Nachfolger Gateway API umziehst und worauf du achten musst.

## Warum das wichtig ist

Wenn jemand deine Website aufruft, landet die Anfrage zuerst bei einem **Ingress-Controller**. Das ist ein Proxy in deinem Cluster, der sich bei jeder Anfrage Hostname und Pfad anschaut und sie an die richtige App weiterleitet. Konfiguriert wird er über **Ingress**-Objekte: „Anfragen an `shop.example.com/api` gehen an die API, alles andere an den Storefront.“

Der beliebteste Ingress-Controller war über Jahre ingress-nginx. Im [November 2025](https://kubernetes.io/blog/2025/11/11/ingress-nginx-retirement/) hat die Netzwerk-Gruppe von Kubernetes (SIG Network) sein Ende angekündigt. Die Wartung lief im März 2026 aus, und seitdem gibt es keine neuen Releases, keine Bugfixes und keine Sicherheitsupdates mehr.

Das Tückische daran: Kaputt gegangen ist nichts. Bestehende Installationen laufen einfach weiter. In vielen Clustern ist damit genau die Komponente, die jede einzelne Anfrage aus dem Internet annimmt, nicht mehr gewartet, und niemand hat es gemerkt.

Welche Möglichkeiten gibt es? Die Ingress-*API* selbst bleibt, nur dieser Controller ist weg. Du könntest auf einen anderen Ingress-Controller wechseln. ingress-nginx wird aber über viele spezielle Annotationen `nginx.ingress.kubernetes.io/*` konfiguriert, und andere Controller verstehen die nicht. Dazu kommt, dass die Ingress-API keine neuen Funktionen mehr bekommt. Wenn du deine Routing-Konfiguration ohnehin neu schreiben musst, lohnt es sich, sie gleich für den Nachfolger zu schreiben: die [Gateway API](https://gateway-api.sigs.k8s.io/).

## Was wir vorhaben

Wir ziehen einen kleinen Shop um. Er besteht aus zwei Apps hinter einem Hostnamen, `shop.example.com`: einem Storefront (der Website) und einer API unter `/api`. Wir gehen in fünf Schritten vor:

1. Den jetzigen Aufbau mit ingress-nginx anschauen, inklusive eines versteckten Verhaltens, das man leicht übersieht.
2. Die drei Bausteine der Gateway API kennenlernen.
3. Den Aufbau übersetzen und den Fehler finden, den eine typische Migration einbaut.
4. Einen Canary-Release ausprobieren, der mit der Gateway API viel einfacher ist.
5. Den häufigsten Fehler nach einer Migration verstehen: eine Route, die sich nicht anhängt.

Am Ende gibt es einen Plan, wie du einen echten Cluster ohne Ausfallzeit migrierst.

Wie im [Probes-Post](/de/posts/readiness-vs-liveness-probes) läuft jedes Manifest auf einem simulierten Cluster in deinem Browser. Klick auf **▶ kubectl apply** und schick dann über die **curl**-Leiste unter dem Block Anfragen. Die Buttons unter der Leiste sind fertige Anfragen, du kannst aber auch eigene eintippen. Der Simulator bringt zwei Proxies mit: einen, der sich wie ingress-nginx verhält, und eine Gateway-API-Implementierung namens `webernetes`.

## Schritt 1: Der Ausgangspunkt, ein Ingress mit Annotationen

So sehen viele echte Ingress-Konfigurationen aus. Ein Detail ist wichtig: Die API erwartet ihre Anfragen unter `/users`, nicht unter `/api/users`. Das Präfix `/api` gibt es nur, damit der Proxy weiß, wohin die Anfrage soll. Es muss also entfernt werden, bevor die Anfrage bei der API ankommt. Bei ingress-nginx macht man das mit zwei Annotationen: `use-regex` erlaubt einen regulären Ausdruck als Pfad, und `rewrite-target: /$2` ersetzt den Pfad durch den Teil nach `/api/`.

Die Apps sind einfache Pods mit je einem Service, damit die Manifeste kurz bleiben. Wende den Block an und klick alle drei Anfragen an.

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

Alle drei Anfragen funktionieren. Die dritte lohnt einen zweiten Blick: Auch `/API/users` in Großbuchstaben erreicht die API. Warum?

ingress-nginx erzeugt pro Hostname einen nginx-Konfigurationsblock. Sobald *ein* Ingress für diesen Hostnamen Regex oder `rewrite-target` nutzt, behandelt nginx *jeden* Pfad dieses Hostnamens als Regex, die Groß- und Kleinschreibung ignoriert. Das gilt sogar für den Ingress `storefront`, der gar keine Annotationen hat.

Niemand hat dieses Verhalten bewusst aufgeschrieben, aber deine Nutzer verlassen sich vielleicht darauf. Genau solches Verhalten geht bei einer Migration still und leise verloren.

## Schritt 2: Die drei Bausteine der Gateway API

Ein Ingress erledigt alles in einem Objekt. Die Gateway API teilt dieselbe Aufgabe auf drei Objekte auf. Die Aufteilung folgt dem, wer in einer typischen Firma wofür zuständig ist:

* **GatewayClass** benennt die Implementierung, also die Software, die die Anfragen tatsächlich weiterleitet. Sie wird vom Cluster-Betreiber installiert. Beispiele sind Envoy Gateway, Cilium, Istio und kgateway. In unserem Simulator heißt sie `webernetes`.
* **Gateway** ist der Eingang: welche Ports, Protokolle und Hostnamen angenommen werden. Darum kümmert sich meistens das Plattform-Team.
* **HTTPRoute** enthält die Routing-Regeln für eine App und gehört dem Team, das die App betreibt. Über `parentRefs` gibt sie an, zu welchem Gateway sie gehört.

Ein weiterer wichtiger Unterschied: Pfad-Umschreibungen, Header-Matches und Traffic-Aufteilungen sind keine Annotationen mit freiem Text mehr. Es sind echte Felder in der Spezifikation. Kubernetes kann sie deshalb schon beim Anwenden prüfen, und sie bedeuten in jeder Implementierung dasselbe.

Den ersten Entwurf musst du nicht von Hand schreiben. Das Tool [ingress2gateway](https://github.com/kubernetes-sigs/ingress2gateway), das im März 2026 Version 1.0 erreicht hat, liest deine Ingresses direkt aus dem Cluster und übersetzt sie:

```sh
ingress2gateway print --providers=ingress-nginx -A
```

Es übersetzt die gängigen Annotationen und warnt bei allem, was es nicht übersetzen kann. Nimm diese Warnungen ernst, denn eine davon betrifft unseren Shop: `rewrite-target` mit Capture-Gruppen wie `$2` wird nicht übersetzt.

## Schritt 3: Den Shop übersetzen und den Fehler finden

Hier ist der Shop auf der Gateway API, so wie er aussieht, wenn jemand diese Warnung übersprungen hat. Statt zwei Ingresses gibt es jetzt ein Gateway und eine HTTPRoute. Die Route hat zwei Regeln: Pfade, die mit `/api` beginnen, gehen an die API, alles andere an den Storefront.

Wende den Block an und schick die drei Anfragen noch einmal.

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

`/api/users` endet jetzt in einer `404`. Die Fehlermeldung verrät, warum: Die API bekommt `/api/users`, weil niemand mehr das Präfix `/api` entfernt.

Um das zu beheben, füg der ersten Regel einen `URLRewrite`-Filter hinzu, direkt über `backendRefs` und mit derselben Einrückung, und wende erneut an:

```yaml
      filters:
        - type: URLRewrite
          urlRewrite:
            path:
              type: ReplacePrefixMatch
              replacePrefixMatch: /
```

`ReplacePrefixMatch` ersetzt genau den Teil des Pfads, den die Regel `PathPrefix` getroffen hat, hier `/api`, durch `/`. Kein regulärer Ausdruck, keine Capture-Gruppen, und jeder, der es liest, versteht, was passiert. In der Spezifikation der Gateway API hat `URLRewrite` den Support-Level „Extended“. Das heißt, nicht jede Implementierung muss es anbieten. Prüf also, ob deine es kann. Die großen können es.

Und `/API/users`? Landet jetzt beim Storefront, weil Pfade in der Gateway API Groß- und Kleinschreibung unterscheiden. Das ist von der Spezifikation so gewollt und meistens auch das, was du willst. Wenn aber irgendwo ein Client deine API in Großbuchstaben aufruft, merkst du das erst nach der Umstellung. Durchsuch deshalb *vor* der Migration deine Access-Logs nach solchen Pfaden.

## Schritt 4: Canary-Releases ohne zweiten Ingress

Bei einem **Canary-Release** schickst du zuerst nur einen kleinen Teil des Traffics an eine neue Version. Funktioniert sie, erhöhst du den Anteil Schritt für Schritt. Wenn nicht, sind nur wenige Nutzer betroffen.

Mit ingress-nginx brauchst du dafür einen zweiten Ingress mit demselben Host und Pfad plus die Annotationen `canary: "true"` und `canary-weight`. Mit der Gateway API setzt du einfach Gewichte an die `backendRefs` einer einzigen Route. Die Route unten schickt 90 % der Anfragen an Version 1 und 10 % an Version 2. Zusätzlich bekommen Tester, die den Header `x-canary: always` mitschicken, immer Version 2.

Wende den Block an und klick die erste Anfrage ein paarmal an. Die meisten Antworten kommen von v1, ab und zu eine von v2. Die zweite Anfrage landet immer bei v2.

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

Ändere die Gewichte auf `50` und `50` und wende erneut an: Die Aufteilung ändert sich sofort. Ein Rollout wird so zu einer Reihe kleiner Änderungen an einem einzigen Objekt, die sich jeweils leicht prüfen lassen. Das passt gut zu GitOps, wo jede Änderung über einen Pull Request läuft.

## Schritt 5: Wenn sich die Route einfach nicht anhängt

Die häufigste Frage nach einer Migration: „Ich habe die HTTPRoute angewendet, warum kommt überall 404?“ Meistens hat sich die Route nicht an das Gateway angehängt, und `kubectl apply` warnt dich nicht davor. Das YAML ist gültig, also wird es angenommen. Ob das Gateway die Route auch wirklich benutzt, zeigt sich erst später im Status der Route.

In diesem Beispiel hat das Plattform-Team das Gateway auf Hostnamen beschränkt, die zu `*.example.com` passen. Die Route will aber `example.com` bedienen. Wende den Block an, schau dir die Statuszeile der Route unter dem Block an und probier beide Anfragen aus.

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

Der Status zeigt `Accepted False (NoMatchingListenerHostname)`. Die Wildcard `*.example.com` deckt `www.example.com` ab, aber nicht die nackte Domain `example.com` selbst. Es gibt zwei Lösungen: Füg dem Gateway einen zweiten Listener mit `hostname: example.com` hinzu, oder stell die Route auf `www.example.com` um.

Auf einem echten Cluster zeigt `kubectl describe httproute <name>` dieselbe Information unter `status.parents`. Mach die Prüfung zu einem festen Teil jeder Migration. Im Terminal des Simulators (`~`) funktionieren auch `kubectl get gateways` und `kubectl get httproutes -n live-4`.

## Ein Migrationsplan ohne Wartungsfenster

So kannst du einen echten Cluster Schritt für Schritt und ohne Ausfallzeit umziehen:

1. **Bestandsaufnahme.** Finde heraus, welche Annotationen du wirklich nutzt:
   `kubectl get ingress -A -o yaml | grep -o 'nginx.ingress.kubernetes.io/[a-z-]*' | sort | uniq -c`
   Die meiste Aufmerksamkeit brauchen `configuration-snippet` und `server-snippet`. Für sie gibt es in der Gateway API kein Gegenstück. Außerdem erlauben sie jedem, der einen Ingress anlegen darf, rohe nginx-Konfiguration einzuschleusen, was über die Jahre zu mehreren Sicherheitslücken geführt hat. Auch [IngressNightmare](https://kubernetes.io/blog/2025/03/24/ingress-nginx-cve-2025-1974/) hat 2025 Konfiguration über Annotationen eingeschleust. Du wirst sie nicht vermissen.
2. **Übersetzen.** Erstell mit `ingress2gateway print` einen ersten Entwurf und arbeite dann jede Warnung von Hand ab, vor allem Regex-Pfade und Rewrites.
3. **Beides parallel betreiben.** Das Gateway bekommt eine eigene IP-Adresse, deine Services und Pods bleiben genau so, wie sie sind. Teste den neuen Weg, bevor du DNS umstellst, zum Beispiel mit `curl --resolve shop.example.com:443:<gateway-ip> https://shop.example.com/...`. Damit geht die Anfrage an das Gateway, obwohl DNS noch auf den alten Controller zeigt.
4. **Schrittweise umschalten.** Senk zuerst die DNS-TTL (wie lange Clients sich die Adresse merken), stell dann DNS auf das Gateway um und beobachte die Fehlerrate. Lass ingress-nginx noch eine Weile laufen, damit du bei Bedarf zurückschalten kannst.
5. **Aufräumen.** Erst dann löschst du die Ingresses und ingress-nginx selbst.

## Zusammenfassung

* **Ungewartet ist nicht dasselbe wie kaputt.** ingress-nginx läuft weiter, und genau deshalb wird es vergessen. Setz die Migration auf deine Roadmap, bevor die nächste Sicherheitslücke dich dazu zwingt.
* **Annotationen sind Verhalten.** Jede Annotation ist eine Funktion, die einen Ersatz oder eine bewusste Entscheidung braucht, sie wegzulassen. Achte auch auf verstecktes Verhalten, wie Regex-Pfade, die Groß- und Kleinschreibung ignorieren.
* **Prüf den `status`, nicht nur `kubectl apply`.** Eine HTTPRoute, die nicht akzeptiert wurde, ist gültiges YAML, das nichts tut.
* **Nutz die Aufteilung der Zuständigkeiten.** Das Plattform-Team verwaltet Gateways, App-Teams verwalten ihre HTTPRoutes, und niemand braucht mehr Zugriff auf die Konfiguration des Controllers.
