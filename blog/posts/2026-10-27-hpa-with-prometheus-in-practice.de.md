# Autoscaling mit Prometheus-Metriken in der Praxis

Der HPA kann Prometheus nicht selbst abfragen. Dieser Post zeigt Schritt für Schritt, wie du beides im echten Cluster verbindest, mit fertigem YAML für Requests pro Sekunde, Queue-Länge und Scale to Zero.

## Das Problem: Der HPA spricht kein PromQL

Im [Post zu Kubernetes v1.37](/de/posts/kubernetes-v1-37-live) haben wir einen Queue-Worker auf null Pods herunter- und wieder hochskaliert. Der **HorizontalPodAutoscaler** (HPA), also der Controller in Kubernetes, der die Anzahl der Pods an die Last anpasst, hat dabei die Anzahl der wartenden Aufträge in einer Queue beobachtet. Im Simulator war diese Zahl einfach da. Am Ende haben wir gesagt, dass du auf einem echten Cluster einen Adapter wie prometheus-adapter oder KEDA brauchst, der sie bereitstellt. Wie das aussieht, haben wir aber nicht gezeigt.

Genau diese Lücke schließt dieser Post. In vielen Clustern läuft schon **Prometheus**, ein Monitoring-System, das regelmäßig Zahlen von deinen Apps einsammelt und als Zeitreihen speichert. Prometheus weiß, wie viele Requests deine API bearbeitet und wie viele Nachrichten in deiner Queue warten. Das sind oft viel bessere Signale zum Skalieren als die CPU-Auslastung:

* Eine API, die meistens auf ihre Datenbank wartet, braucht kaum CPU, auch wenn sie überlastet ist. Requests pro Sekunde zeigen die Last direkt.
* Ein Queue-Worker sollte mit der Menge an Arbeit skalieren, die *wartet*, nicht damit, wie beschäftigt die vorhandenen Worker sind. Und bei null Pods gibt es gar keine CPU, die man messen könnte.

Der Haken: Der HPA kann Prometheus nicht abfragen. Er liest Metriken nur aus drei festen Kubernetes-APIs:

| API | Was sie liefert | Wer sie meistens bereitstellt |
| --- | --- | --- |
| `metrics.k8s.io` | CPU und Arbeitsspeicher von Pods und Nodes | metrics-server |
| `custom.metrics.k8s.io` | Zahlen, die zu einem Kubernetes-Objekt gehören, z. B. Requests pro Sekunde *eines Pods* | prometheus-adapter |
| `external.metrics.k8s.io` | Zahlen von außerhalb der Cluster-Objekte, z. B. die Länge einer Queue | prometheus-adapter oder KEDA |

Die letzten beiden implementiert Kubernetes nicht selbst. Es legt nur fest, wie sie aussehen. Zwischen Prometheus und dem HPA muss also etwas sitzen, das die Frage des HPA entgegennimmt („Welchen Wert hat `http_requests_per_second` für die Pods im Namespace `shop`?“), daraus eine Prometheus-Abfrage macht und die Antwort zurückgibt. Dieses Etwas heißt **Metrics-Adapter**.

Die ganze Kette sieht so aus:

1. Deine App stellt eine Zahl über einen HTTP-Endpunkt bereit, meistens `/metrics`.
2. Prometheus holt sie alle paar Sekunden ab. Das nennt man **Scraping**.
3. Der Adapter übersetzt die Fragen des HPA in Prometheus-Abfragen.
4. Der HPA vergleicht die Antwort mit deinem Zielwert und fügt Pods hinzu oder entfernt welche.

Fehlt ein Glied, zeigt der HPA statt eines Werts `<unknown>` an und skaliert nicht mehr. Das ist in der Praxis das häufigste Problem. Deshalb gehen wir jedes Glied einzeln durch und prüfen es, bevor es weitergeht.

## Was wir machen

Wir nehmen zwei typische Workloads:

* `shop-api`, eine Web-API im Namespace `shop`. Sie soll nach **Requests pro Sekunde** skalieren.
* `image-resizer`, ein Queue-Worker im Namespace `images`. Er holt sich Aufträge aus der RabbitMQ-Queue `image-resize` und soll nach **Queue-Länge** skalieren, bis hinunter auf null.

Die Schritte:

1. Die Metrik der API zu Prometheus bringen.
2. Die Prometheus-Abfrage schreiben und testen.
3. prometheus-adapter installieren und ihm die Metrik beibringen.
4. Die API nach Requests pro Sekunde skalieren.
5. Den Worker nach Queue-Länge skalieren, bis auf null, und live sehen, was passiert, wenn der Name der Metrik falsch ist.
6. Dasselbe mit KEDA statt prometheus-adapter.
7. Entscheiden, welches der beiden Tools du brauchst, und die typischen Stolperfallen umgehen.

In diesem Post geht es um einen echten Cluster. Die meisten Blöcke sind deshalb normales YAML, das du mit `kubectl` oder Helm anwendest. Wir gehen davon aus, dass Prometheus mit dem Helm-Chart [kube-prometheus-stack](https://github.com/prometheus-community/helm-charts/tree/main/charts/kube-prometheus-stack) installiert ist, mit dem Release-Namen `kube-prometheus-stack` im Namespace `monitoring`. Sieht dein Setup anders aus, musst du vor allem die Adresse von Prometheus und ein Label anpassen. In Schritt 5 gibt es einen Live-Block, der wie in den früheren Posts auf dem Simulator in deinem Browser läuft.

## Schritt 1: Die Metrik zu Prometheus bringen

Bevor irgendetwas nach einer Zahl skalieren kann, muss Prometheus sie einsammeln. Unsere `shop-api` stellt bereits einen **Counter** namens `http_requests_total` auf Port 8080 unter `/metrics` bereit. Ein Counter ist eine Zahl, die nur steigt: Jeder Request zählt eins dazu. Allein sagt er wenig aus („1.384.021 Requests seit dem Start des Pods“). Aber daraus, wie schnell er wächst, kann Prometheus die Requests pro Sekunde berechnen. Das siehst du in Schritt 2.

Für die meisten Web-Frameworks gibt es eine Prometheus-Bibliothek, die so einen Counter für dich bereitstellt. Prüfen kannst du das mit `curl localhost:8080/metrics` im Pod. Du solltest eine Zeile wie diese sehen:

```text
http_requests_total{method="GET",code="200"} 1384021
```

Als Nächstes muss Prometheus wissen, wo es den Endpunkt findet. Bei kube-prometheus-stack sagst du ihm das mit einem **ServiceMonitor**. Das ist ein kleines Objekt, das sagt: „Scrape alle Pods hinter den Services mit diesen Labels.“ Leg ihn im Namespace der App an:

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

Was die Felder bedeuten:

* `selector` wählt den Service von `shop-api` über sein Label `app: shop-api` aus.
* `port: http` ist der *Name* des Ports in diesem Service, nicht die Nummer. Hat dein Service-Port keinen Namen, gib ihm einen.
* `interval: 15s` heißt, dass Prometheus die Zahlen alle 15 Sekunden abholt.
* Das Label `release: kube-prometheus-stack` vergisst man leicht. Das Prometheus aus kube-prometheus-stack beachtet standardmäßig nur ServiceMonitors mit diesem Label. Ohne das Label existiert der ServiceMonitor zwar, aber es passiert nichts, und nirgends erscheint eine Fehlermeldung.

**So prüfst du es:** Öffne die Prometheus-Oberfläche mit einem Port-Forward:

```sh
kubectl port-forward -n monitoring svc/kube-prometheus-stack-prometheus 9090
```

Ruf dann `http://localhost:9090/targets` im Browser auf. Nach etwa einer halben Minute solltest du einen Eintrag `serviceMonitor/shop/shop-api/0` sehen, mit einer Zeile pro Pod und dem Zustand `UP`. Fehlt er, prüf zuerst das Label `release`.

Prometheus hängt an jede Zahl, die es so einsammelt, automatisch Labels an, darunter `namespace` und `pod`. Diese beiden brauchen wir in Schritt 3.

## Schritt 2: Die Abfrage schreiben und testen

Bevor der HPA eine Abfrage benutzt, solltest du dir ihr Ergebnis selbst ansehen. Öffne in der Prometheus-Oberfläche den Tab **Graph** und gib ein:

```promql
sum by (pod) (rate(http_requests_total{namespace="shop"}[2m]))
```

Lies sie von innen nach außen:

* `http_requests_total{namespace="shop"}` wählt die Counter aller Pods im Namespace `shop` aus.
* `rate(…[2m])` macht aus jedem Counter „Requests pro Sekunde, gemittelt über die letzten zwei Minuten“.
* `sum by (pod) (…)` addiert die verschiedenen Kombinationen aus `method` und `code`, sodass du genau eine Zahl pro Pod bekommst.

Du solltest eine Linie pro Pod sehen. Schick etwas Traffic an die API, dann sollten die Linien innerhalb einer Minute steigen.

Ein Hinweis zu `[2m]`: `rate` braucht mindestens zwei Messwerte im Zeitfenster, besser ein paar mehr. Als Faustregel gilt das Vierfache des Scrape-Intervalls. Bei 15 Sekunden ist eine Minute das Minimum, mit zwei Minuten bist du auf der sicheren Seite. Ein längeres Fenster macht den Wert ruhiger, aber auch träger.

## Schritt 3: prometheus-adapter installieren und ihm die Metrik beibringen

[prometheus-adapter](https://github.com/kubernetes-sigs/prometheus-adapter) ist der Metrics-Adapter aus dem Kubernetes-Projekt. Er stellt `custom.metrics.k8s.io` und `external.metrics.k8s.io` bereit und beantwortet jede Frage mit einer Prometheus-Abfrage. Welche Metriken er unter welchem Namen anbietet, legen **Regeln** (Rules) in seiner Konfiguration fest.

Speichere das hier als `adapter-values.yaml`:

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

Was die Teile machen:

* `prometheus.url` und `port` sagen, wo Prometheus zu finden ist. Das ist der Service, den kube-prometheus-stack anlegt.
* `default: false` schaltet die eingebauten Regeln des Charts ab. Sie würden Hunderte Metriken anbieten, nach denen niemand skaliert. Mit nur deinen eigenen Regeln bleibt der Adapter schnell und seine Liste von Metriken übersichtlich.
* `seriesQuery` sagt, um welche Prometheus-Zeitreihen es in dieser Regel geht: `http_requests_total`, aber nur dort, wo die Labels `namespace` und `pod` gesetzt sind.
* `resources.overrides` verknüpft Prometheus-Labels mit Kubernetes-Objekten: Das Label `namespace` ist ein Namespace, das Label `pod` ist ein Pod. Erst dadurch wird es eine Metrik *eines Pods*, nach der der HPA fragen kann.
* `name` legt den Namen fest, den der HPA benutzt. Der reguläre Ausdruck schneidet `_total` ab und hängt `_per_second` an, der HPA fragt also nach `http_requests_per_second`. Die Gesamtzahl eines Counters hilft beim Skalieren nicht, eine Rate schon, und der Name soll das auch sagen.
* `metricsQuery` ist die Abfrage aus Schritt 2, als Vorlage geschrieben. Wenn der HPA fragt, setzt der Adapter `<<.Series>>` (den Namen der Metrik), `<<.LabelMatchers>>` (den Namespace und die Pods des Deployments) und `<<.GroupBy>>` (hier: `pod`) ein.

Installiere den Adapter mit Helm in denselben Namespace wie Prometheus:

```sh
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm install prometheus-adapter prometheus-community/prometheus-adapter \
  -n monitoring -f adapter-values.yaml
```

**So prüfst du es:** Frag die API direkt, genau wie der HPA es später tut. Der `*` steht für „alle Pods im Namespace“:

```sh
kubectl get --raw "/apis/custom.metrics.k8s.io/v1beta1/namespaces/shop/pods/*/http_requests_per_second"
```

Die Antwort ist JSON mit einem Eintrag pro Pod. Der interessante Teil sieht so aus:

```json
{
  "describedObject": { "kind": "Pod", "name": "shop-api-7c9f8b6d4-x2lq8" },
  "metric": { "name": "http_requests_per_second" },
  "value": "4200m"
}
```

Lass dich von `4200m` nicht irritieren. Kubernetes schreibt Zahlen als **Quantities**, im selben Format wie CPU-Requests. Das `m` steht für „milli“, `4200m` sind also 4,2 Requests pro Sekunde.

Bekommst du stattdessen `NotFound`, hat der Adapter keine passenden Zeitreihen gefunden. Prüf die `seriesQuery` in der Prometheus-Oberfläche und sieh dir das Log des Adapters mit `kubectl logs -n monitoring deploy/prometheus-adapter` an.

## Schritt 4: Die API nach Requests pro Sekunde skalieren

Jetzt der HPA. Wir verwenden den Metrik-Typ `Pods`. Er bedeutet: „Frag diese Metrik für jeden Pod des Deployments ab und nimm den Durchschnitt.“ Mit `averageValue: "50"` peilt der HPA 50 Requests pro Sekunde pro Pod an.

Die Anzahl der Pods berechnet der HPA so: *aktuelle Pods × aktueller Wert ÷ Zielwert*, aufgerundet. Drei Pods mit durchschnittlich 80 Requests pro Sekunde ergeben 3 × 80 ÷ 50 = 4,8, also fünf Pods. Sinkt die Last auf 20 pro Pod, sind es 3 × 20 ÷ 50 = 1,2, also zwei Pods.

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

Die Überlegungen hinter den Zahlen:

* **Woher kommt die 50?** Aus einem Lasttest. Find heraus, wie viele Requests pro Sekunde ein Pod schafft, bevor seine Antwortzeiten schlechter werden, und nimm etwa 70 % davon als Ziel. Die restlichen 30 % sind Reserve, solange neue Pods noch starten.
* **`minReplicas: 2`**: Eine API, die Kunden benutzen, sollte nicht an einem einzigen Pod hängen. Stürzt er ab oder wird sein Node neu gestartet, antwortet der zweite weiter.
* **`scaleUp`-Policy**: Der HPA darf die Anzahl der Pods höchstens einmal pro Minute verdoppeln (100 % pro 60 Sekunden). Das ist schnell genug für echte Last und schützt dich davor, dass ein Messfehler dich direkt auf `maxReplicas` hochjagt.
* **`scaleDown`-Fenster**: Bevor der HPA Pods entfernt, nimmt er die höchste Empfehlung der letzten fünf Minuten. Traffic kommt oft in Wellen, und so werden Pods nicht in einer kurzen Delle entfernt und gleich danach wieder gestartet.
* Wie im v1.37-Post: **Entferne `replicas` aus dem Deployment**, sonst setzt jedes `kubectl apply` die Anzahl der Pods zurück.

Wende den HPA an und prüf ihn:

```sh
kubectl get hpa -n shop
```

```text
NAME       REFERENCE             TARGETS     MINPODS   MAXPODS   REPLICAS   AGE
shop-api   Deployment/shop-api   4200m/50    2         10        2          1m
```

`TARGETS` zeigt den aktuellen Durchschnitt neben dem Zielwert. Sobald dort eine Zahl steht und nicht `<unknown>`, funktioniert die ganze Kette.

**Tipp:** Du kannst mehrere Metriken angeben, zum Beispiel Requests pro Sekunde *und* CPU. Der HPA berechnet für jede eine Anzahl von Pods und nimmt die höchste. So gewinnt immer die Grenze, die zuerst erreicht ist.

## Schritt 5: Einen Worker nach Queue-Länge skalieren, bis auf null

Jetzt der Worker. Sein Signal ist die Anzahl der Nachrichten, die in der RabbitMQ-Queue `image-resize` warten. Das eingebaute Prometheus-Plugin von RabbitMQ meldet das als `rabbitmq_queue_messages_ready`, mit einem Label `queue` für den Namen der Queue, sobald Metriken pro Queue eingeschaltet sind (`prometheus.return_per_object_metrics = true`). Andere Systeme haben ähnliche Metriken, zum Beispiel den Consumer-Lag bei Kafka.

Diese Zahl gehört zu keinem Pod des Workers. Sie kommt von RabbitMQ, das in einem eigenen Namespace läuft. Genau dafür gibt es **externe Metriken**. Und wie du im v1.37-Post gesehen hast, ist eine externe Metrik auch die Voraussetzung für `minReplicas: 0`: Sie existiert auch dann noch, wenn kein Worker läuft.

Ergänze in deiner `adapter-values.yaml` unter `rules` einen Abschnitt `external`, unterhalb von `custom` und mit derselben Einrückung:

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

Zwei Dinge sind anders als bei der Custom-Regel:

* `namespaced: false`: Normalerweise sucht der Adapter nur nach Zeitreihen, deren Label `namespace` zum Namespace des HPA passt. Unsere Zeitreihe kommt aber von RabbitMQ, ihr Label `namespace` ist also der Namespace, in dem RabbitMQ läuft, nicht `images`. Diese Einstellung schaltet den Filter ab.
* `as: "pending_jobs"` gibt der Metrik einen kurzen, neutralen Namen. Der HPA muss nicht wissen, dass RabbitMQ dahintersteckt. Wechselst du später auf ein anderes Queue-System, ändert sich nur die Regel.

Aktualisiere den Adapter mit `helm upgrade prometheus-adapter prometheus-community/prometheus-adapter -n monitoring -f adapter-values.yaml` und prüf die neue Metrik. Der `labelSelector` am Ende wählt unsere Queue aus (`%3D` ist ein codiertes `=`):

```sh
kubectl get --raw "/apis/external.metrics.k8s.io/v1beta1/namespaces/images/pending_jobs?labelSelector=queue%3Dimage-resize"
```

Dann der HPA. Er ist dem aus dem v1.37-Post sehr ähnlich. Neu ist der `selector`: Der Adapter reicht seine Labels an die Abfrage weiter, sodass der HPA nur die Queue `image-resize` sieht, auch wenn RabbitMQ viele Queues hat.

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

`averageValue: "5"` bedeutet höchstens fünf wartende Nachrichten pro Pod: Bei 30 Nachrichten will der HPA sechs Pods, bei null Nachrichten gar keinen. Das Scale-down-Fenster von zwei Minuten lässt den letzten Worker noch einen Moment laufen, nachdem die Queue leer ist, falls gleich der nächste Schwung kommt.

### Ausprobieren: Was der HPA sieht, wenn der Name falsch ist

Der häufigste Fehler bei diesem Setup ist der Name der Metrik. Der HPA muss den Namen verwenden, den der *Adapter* anbietet, nicht den Namen in Prometheus. Unsere Regel hat `rabbitmq_queue_messages_ready` in `pending_jobs` umbenannt. Den Prometheus-Namen gibt es für den HPA also nicht mehr.

Der Simulator hat einen kleinen eingebauten Adapter, der die Länge einer Auftrags-Queue als `pending_jobs` bereitstellt, genau wie unsere Regel. Das Worker-Image holt sich alle zwei Sekunden einen Auftrag aus der Queue. Der Block unten macht den Fehler absichtlich: Der HPA fragt nach dem Prometheus-Namen. (Der Simulator ignoriert `selector`, deshalb lassen wir ihn hier weg.)

1. Wende den Block an. Das Deployment startet mit einem Pod, und der HPA meldet `ScalingActive: False` mit dem Hinweis, dass die External Metrics API keine Werte geliefert hat. Achte darauf, dass der Pod weiterläuft: Wenn der HPA seine Metrik nicht lesen kann, skaliert er überhaupt nicht, weder hoch noch runter.
2. Öffne mit `~` das Terminal und führe `kubectl get hpa -n live-1` aus. In der Spalte `TARGETS` steht `<unknown>`, genau wie auf einem echten Cluster mit diesem Fehler.
3. Ändere im Block `rabbitmq_queue_messages_ready` in `pending_jobs` und klick noch einmal auf **▶ kubectl apply**. Der Fehler verschwindet, und nach etwa zehn Sekunden skaliert der HPA den untätigen Worker auf null.
4. Klick auf **+10 jobs**. Der HPA startet zwei Worker, und sobald die Queue leer ist, geht es wieder auf null.

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

Auf einem echten Cluster findest du die vollständige Fehlermeldung mit `kubectl describe hpa -n images image-resizer`, unter `Conditions` und `Events` am Ende. Welche externen Metriken der Adapter überhaupt anbietet, zeigt dir `kubectl get --raw /apis/external.metrics.k8s.io/v1beta1`.

## Schritt 6: Dasselbe mit KEDA

[KEDA](https://keda.sh/) (Kubernetes Event-driven Autoscaling) geht einen anderen Weg. Statt Adapter-Regeln und einen HPA zu schreiben, schreibst du ein einziges Objekt, ein **ScaledObject**. Darin sagst du, welches Deployment skaliert werden soll und welche Prometheus-Abfrage dafür zählt. KEDA legt dann den HPA für dich an, stellt die externe Metrik selbst bereit und schickt deine Abfrage direkt an Prometheus. Es gibt keine Regel, die etwas umbenennt, der Namensfehler aus Schritt 5 kann also nicht passieren.

Installiere KEDA mit Helm:

```sh
helm repo add kedacore https://kedacore.github.io/charts
helm install keda kedacore/keda -n keda --create-namespace
```

Dann das ScaledObject für unseren Worker. Wenn du Schritt 5 mitgemacht hast, lösch vorher den HPA `image-resizer`, denn KEDA weigert sich, ein Deployment zu übernehmen, das schon ein anderer HPA skaliert.

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

So passt das zu dem, was du schon kennst:

* `minReplicaCount` und `maxReplicaCount` entsprechen `minReplicas` und `maxReplicas` beim HPA.
* `query` ist eine normale PromQL-Abfrage. Sie muss genau eine Zahl liefern. Deshalb steht `sum(…)` drumherum, obwohl es nur eine Queue gibt.
* `threshold: "5"` ist der Zielwert pro Pod, wie `averageValue: "5"` beim HPA.
* `pollingInterval: 15` heißt, dass KEDA alle 15 Sekunden bei Prometheus nachfragt, ob Arbeit da ist.
* `cooldownPeriod: 120` ist die Zeit, die die Queue leer bleiben muss, bevor KEDA von einem Pod auf null geht.
* `fallback` gibt es beim normalen HPA nicht: Schlägt die Abfrage dreimal hintereinander fehl, zum Beispiel weil Prometheus nicht erreichbar ist, skaliert KEDA auf zwei Pods. Dein Worker arbeitet weiter, auch wenn gerade niemand messen kann, wie viel Arbeit da ist.

Ein wichtiger Unterschied: Den Schritt zwischen null und einem Pod macht KEDA selbst. Der HPA, den es anlegt, kümmert sich nur um einen Pod und mehr. Deshalb funktioniert Scale to Zero mit KEDA auch auf Clustern, die älter als v1.37 sind. Außerdem kann KEDA viele Quellen auch ohne Prometheus lesen, darunter RabbitMQ, Kafka, SQS und Zeitpläne (Cron).

**So prüfst du es:** `kubectl get scaledobject -n images` sollte `READY True` und, solange die Queue leer ist, `ACTIVE False` zeigen. Der HPA von KEDA heißt `keda-hpa-image-resizer` und taucht wie jeder andere HPA in `kubectl get hpa -n images` auf.

## Schritt 7: Welches Tool, und worauf du achten solltest

**prometheus-adapter oder KEDA?** Beides funktioniert, und am Ende steht in beiden Fällen ein normaler HPA.

* Nimm **prometheus-adapter**, wenn du hauptsächlich nach Metriken deiner Pods skalierst, etwa Requests pro Sekunde, und einfache HPA-Objekte willst. Es ist eine kleine Komponente, und jeder HPA sieht aus wie die in der Kubernetes-Dokumentation.
* Nimm **KEDA**, wenn du nach Queues und anderen Systemen skalierst, Scale to Zero auf Clustern vor v1.37 brauchst oder einen `fallback` willst. Statt Adapter-Regeln schreibst du eine PromQL-Abfrage.

**Externe Metriken kann nur einer von beiden bereitstellen.** Jede der drei Metrik-APIs wird im Cluster mit einem **APIService**-Objekt registriert, und davon gibt es pro API nur eines. KEDA registriert immer `external.metrics.k8s.io`. Das Chart von prometheus-adapter tut dasselbe, sobald du `external`-Regeln definierst. Installierst du beide so, streiten sie sich um dasselbe Objekt: Entweder schlägt die zweite Installation fehl, oder einer übernimmt die API, und die HPAs des anderen zeigen `<unknown>`. Kombinieren lassen sich beide so: KEDA stellt die externen Metriken bereit, und prometheus-adapter hat nur `custom`-Regeln. Wer die API gerade bedient, siehst du mit:

```sh
kubectl get apiservice v1beta1.external.metrics.k8s.io
```

**Fällt Prometheus aus, steht der HPA still.** Er kann nichts berechnen und behält die aktuelle Anzahl von Pods. Das ist ein vernünftiges Verhalten, bei null Pods heißt es aber: Der Worker startet nicht, egal wie voll die Queue wird. Bei prometheus-adapter solltest du deshalb einen Alarm einrichten: Ein HPA, dessen Condition `ScalingActive` auf `False` steht, sollte jemanden benachrichtigen. Bei KEDA deckt der `fallback` diesen Fall ab.

**Die Kette hat eine Verzögerung.** Scraping (15 Sekunden), das `rate`-Fenster, der Adapter und das eigene Sync-Intervall des HPA (15 Sekunden) addieren sich. Rechne damit, dass der HPA etwa 30 bis 60 Sekunden nach einer Laständerung reagiert, plus die Zeit, die ein neuer Pod zum Starten braucht. Plötzliche Spitzen müssen die Pods abfangen, die schon laufen. Dafür sind die Reserve in deinem Zielwert und `minReplicas` da.

## Zusammenfassung

* Der HPA kann Prometheus nicht abfragen. Er liest `custom.metrics.k8s.io` und `external.metrics.k8s.io`, und ein **Metrics-Adapter** beantwortet diese Fragen mit Prometheus-Abfragen.
* Prüf jedes Glied der Kette einzeln: das Target in der Prometheus-Oberfläche, die Abfrage im Graph-Tab, die Metrik mit `kubectl get --raw` und zum Schluss `TARGETS` in `kubectl get hpa`.
* Nimm **`Pods`-Metriken** für Werte deiner eigenen Pods, etwa Requests pro Sekunde, und **`External`-Metriken** für Werte von außerhalb, etwa die Queue-Länge. Nur externe (oder Object-)Metriken erlauben `minReplicas: 0`.
* Der HPA verwendet den Namen, den der **Adapter** anbietet, nicht den Prometheus-Namen. Ein falscher Name zeigt sich als `<unknown>`, und dann skaliert der HPA gar nicht.
* **KEDA** fasst Adapter und HPA in einem ScaledObject mit einer einfachen PromQL-Abfrage zusammen, übernimmt Scale to Zero selbst und hat einen `fallback` für den Fall, dass Prometheus nicht erreichbar ist.
* Nur eine Komponente kann `external.metrics.k8s.io` bereitstellen. Nutzt du KEDA und prometheus-adapter zusammen, überlass die externen Metriken KEDA.
