# Kubernetes v1.37, live

Kubernetes v1.37 kann ungenutzte Workloads komplett herunterfahren und laufenden Pods mehr CPU geben, ohne sie neu zu starten. In diesem Post probierst du beides Schritt für Schritt live im Browser aus.

## Was neu ist und was wir ausprobieren

[Kubernetes v1.37](https://kubernetes.io/blog/2026/08/26/kubernetes-v1-37-release/) ist im August 2026 erschienen. Viele der Änderungen haben dasselbe Ziel: CPU und Arbeitsspeicher effizienter zu nutzen. Wir probieren die beiden aus, die du im Alltag am meisten merkst:

1. **Scale to Zero mit dem HorizontalPodAutoscaler** (Beta, standardmäßig aktiv). Eine App, die nichts zu tun hat, kann jetzt auf null Pods herunterskaliert werden und kommt automatisch zurück, sobald es Arbeit gibt.
2. **Scheduler-Preemption für In-Place-Pod-Resize** (Alpha). Einem laufenden Pod ohne Neustart mehr CPU zu geben, funktioniert seit v1.35. Neu in v1.37: Ist der Node voll, kann Kubernetes Platz schaffen, indem es weniger wichtige Pods entfernt.

Am Ende gibt es noch einen kurzen Blick auf zwei weitere Änderungen, Pod-Level-Resource-Manager und Memory QoS. Die arbeiten im Inneren des Nodes und lassen sich im Browser kaum zeigen.

Wie in den [früheren Posts](/de/posts/kubernetes-live-in-your-browser) läuft jedes Manifest unten auf einem simulierten Cluster in deinem Browser. Klick auf **▶ kubectl apply**, um es zu starten, und beobachte dann den Status unter dem Block. Unter manchen Blöcken gibt es fertige `kubectl`-Befehle, die du mit einem Klick ausführen kannst. Mit `~` öffnest du ein Terminal. Jeder Block läuft in einem eigenen Namespace, der in seinem Kopf steht.

## Teil 1: Scale to Zero

### Das Problem: Auch Pods, die nichts tun, kosten Geld

Stell dir einen Worker vor, der Aufträge aus einer Warteschlange (Queue) abarbeitet, zum Beispiel hochgeladene Bilder verkleinert. Tagsüber gibt es viel zu tun, nachts gar nichts.

Der **HorizontalPodAutoscaler** (HPA) passt die Anzahl der Pods an die Last an. Er misst regelmäßig eine Metrik, vergleicht sie mit einem Zielwert und fügt Pods hinzu oder entfernt welche. Bisher konnte er aber nie unter einen Pod gehen. Mindestens ein Worker lief also die ganze Nacht und wartete auf Aufträge, die nie kamen. Bei vielen solchen Workern summiert sich das.

Seit v1.37 kannst du `minReplicas: 0` setzen. Es gibt eine Bedingung, und um sie zu verstehen, lohnt es sich, zuerst den naheliegenden Weg auszuprobieren.

### Schritt 1: Warum CPU als Metrik nicht funktioniert

Die häufigste HPA-Einstellung skaliert nach CPU-Auslastung: „Wenn die Pods mehr als 50 % ihrer CPU nutzen, füge Pods hinzu.“ Probieren wir das mit `minReplicas: 0`. Wende den Block an und lies die Antwort.

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

Das Deployment wird angelegt, aber der API-Server lehnt den HPA ab: `must specify at least one Object or External metric to support scaling to zero replicas`.

Wenn man darüber nachdenkt, ergibt das Sinn. Die CPU-Auslastung wird an laufenden Pods gemessen. Bei null Pods gibt es nichts zu messen, und der HPA würde nie erfahren, dass wieder Arbeit da ist. Er braucht ein Signal, das *außerhalb* der Pods existiert. Dafür gibt es die Metrik-Typen `External` und `Object`, zum Beispiel die Länge einer Queue.

### Schritt 2: Einen Queue-Worker auf null und zurück skalieren

Jetzt derselbe Worker, aber der HPA beobachtet die Anzahl der wartenden Aufträge in der Queue (die externe Metrik `pending_jobs`). Das Ziel `averageValue: "5"` bedeutet: Pro Pod sollen höchstens fünf Aufträge warten. Bei 10 wartenden Aufträgen will der HPA zwei Pods, bei 25 will er fünf und bei null gar keinen.

Zwei weitere Details im Manifest:

* Das Deployment hat **kein Feld `replicas`**. Die Anzahl der Pods bestimmt der HPA. Stünde das Feld im Manifest, würde jedes neue `kubectl apply` (oder dein GitOps-Tool) die Anzahl zurücksetzen und gegen den HPA arbeiten.
* `stabilizationWindowSeconds: 10` sagt dem HPA, dass er zehn Sekunden warten soll, bevor er Pods entfernt. So führt eine kurze Pause in der Queue nicht gleich dazu, dass alles herunterfährt. Standardmäßig sind es fünf Minuten. Wir verkürzen das hier, damit du nicht so lange warten musst.

So gehst du vor:

1. Wende den Block an. Das Deployment startet mit einem Pod. Nach etwa zehn Sekunden entfernt der HPA ihn, und die Statuszeile zeigt `ScaledToZero: True`.
2. Klick auf **+10 jobs**. Der HPA sieht zehn wartende Aufträge und startet zwei Pods.
3. Beobachte die Queue-Zeile: Jeder Worker erledigt alle zwei Sekunden einen Auftrag.
4. Sobald die Queue leer ist, wartet der HPA zehn Sekunden und skaliert wieder auf null.

Klick mehrmals schnell hintereinander auf **+10 jobs**, um zu sehen, wie der HPA bis auf fünf Pods hochgeht (`maxReplicas`).

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

Im Terminal zeigt `kubectl get hpa -n live-2` dieselben Zahlen wie auf einem echten Cluster: den aktuellen Wert der Metrik, den Zielwert und die Anzahl der Pods.

### Was du auf einem echten Cluster brauchst

Der Simulator bringt eine eingebaute Quelle für `pending_jobs` mit. Auf einem echten Cluster fragt der HPA die **External Metrics API** nach dem Wert, und irgendetwas muss ihn dort bereitstellen: ein Adapter wie [prometheus-adapter](https://github.com/kubernetes-sigs/prometheus-adapter) oder [KEDA](https://keda.sh/), der den Wert aus deiner Queue oder deinem Monitoring liest. Mit dem HPA allein wird kein Adapter installiert.

Behalte drei Dinge im Kopf, bevor du einen Workload auf null skalierst:

* **Der erste Auftrag muss warten.** Füllt sich die Queue wieder, muss der HPA das bemerken, und der Pod muss eingeplant werden, sein Image laden und starten. Das kann Sekunden oder Minuten dauern.
* **Die Metrik muss von außerhalb der App kommen.** Eine Metrik, die der Worker selbst meldet, verschwindet zusammen mit dem letzten Pod.
* **Beim Stoppen von Pods darf keine Arbeit verloren gehen.** Ein Auftrag sollte erst dann als erledigt gelten, wenn er wirklich verarbeitet wurde. Die Worker in dieser Demo legen einen Auftrag zurück in die Queue, wenn sie mittendrin gestoppt werden.

## Teil 2: Mehr CPU ohne Neustart

### Hintergrund: In-Place-Pod-Resize

Lange Zeit bedeutete eine Änderung an CPU oder Arbeitsspeicher eines Pods, ihn zu ersetzen: alten Pod löschen, neuen starten. Seit v1.35 kann Kubernetes einen laufenden Pod **an Ort und Stelle** (in place) anpassen. Der Container läuft weiter, der Restart-Zähler bleibt bei null, und die App merkt davon nichts, außer dass ihr jetzt mehr CPU zur Verfügung steht.

Das machst du nicht mit einem normalen `kubectl apply`, sondern über die eigene Subressource `resize`:

```sh
kubectl patch pod shop --subresource resize -p '{"spec":{"containers":[{"name":"app","resources":{"requests":{"cpu":"1"}}}]}}'
```

Das Kubelet (der Kubernetes-Agent auf dem Node) prüft, ob der Node genug freie CPU hat. Wenn ja, übernimmt es den neuen Wert sofort.

### Schritt 3: Einen laufenden Pod vergrößern

Der Pod unten fordert eine halbe CPU an (`500m`, also 500 Millicores). Mit `nodeName` setzen wir ihn auf einen festen Node. In dieser Demo hat jeder Node Platz für 2 CPUs.

1. Wende den Block an und warte, bis der Pod `Running` ist. Die Spalte `CPU` zeigt `500m`.
2. Klick auf den `kubectl patch`-Befehl unter dem Block. Der Pod zeigt kurz `PodResizeInProgress`, danach steht in der Spalte `CPU` eine `1`, und `RESTARTS` ist immer noch `0`.
3. Probier jetzt den anderen Weg: Ändere im Block `cpu: 500m` auf `cpu: 750m` und klick auf **▶ kubectl apply**. Der API-Server lehnt das ab. Die Ressourcen eines laufenden Pods lassen sich nur über die Subressource `resize` ändern, nicht über ein normales Update.

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

### Das Problem: Was, wenn der Node voll ist?

Bisher hatte der Node genug Platz. Aber was passiert, wenn ein Pod mehr CPU will, als auf seinem Node noch frei ist? Bis v1.36 hat das Kubelet den Resize als **Deferred** (zurückgestellt) markiert und gewartet. Wenn auf dem Node nie etwas anderes beendet wurde, wartete der Pod für immer.

Neu in v1.37 (Alpha): Der Scheduler kann jetzt andere Pods **verdrängen** (Preemption), um Platz zu schaffen. Verdrängen heißt, er entfernt Pods, die weniger wichtig sind. Welche Pods weniger wichtig sind, bestimmt ihre **PriorityClass**, eine Zahl, die du einem Pod gibst. Eine höhere Zahl bedeutet wichtiger. Entfernt werden können nur Pods auf demselben Node mit einer *niedrigeren* Priorität.

Das Feature ist Alpha und deshalb standardmäßig ausgeschaltet. Auf einem echten Cluster schaltest du es mit dem Feature-Gate `InPlacePodVerticalScalingSchedulerPreemption` ein. Im Simulator ist es aktiv.

### Schritt 4: Platz schaffen durch Preemption

Zwei Pods teilen sich einen Node mit Platz für 2 CPUs:

* `shop` ist unser wichtiger Webshop mit Priorität `1000` und einer halben CPU.
* `report` ist ein nächtlicher Report-Job mit Priorität `100` und 1,2 CPUs.

Zusammen nutzen sie 1,7 CPUs. Jetzt will der Shop 1,5 CPUs. Das wären insgesamt 2,7, mehr als der Node hat.

1. Wende den Block an und warte, bis beide Pods `Running` sind.
2. Klick auf den ersten Befehl unter dem Block. Der Shop fordert 1,5 CPUs an. Der Resize ist zunächst `Deferred`, weil der Node voll ist.
3. Der Scheduler sieht, dass `report` eine niedrigere Priorität hat, und entfernt ihn (`preempted: report`). Sobald der Report-Pod weg ist, bekommt der Shop seine 1,5 CPUs, immer noch ohne Neustart.
4. Klick auf den zweiten Befehl. Jetzt fordert der Shop 3 CPUs an. Der Status zeigt `Infeasible`: Der Node hat insgesamt nur 2 CPUs, da hilft auch keine Preemption. Kubernetes versucht es gar nicht erst.

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

Zum Vergleich: Klick auf **↺ reset**, ändere die `priorityClassName` von `report` auf `shop` und wende erneut an. Jetzt sind beide Pods gleich wichtig. Für einen Pod mit derselben Priorität entfernt Kubernetes keinen anderen, also bleibt der Resize `Deferred`.

Zwei Dinge solltest du auf einem echten Cluster beachten:

* Der entfernte Pod ist weg. Gehört er zu einem Deployment, wird ein neuer gestartet, eventuell auf einem anderen Node. Ein einzelner Pod wie unser Report-Job endet einfach. Gib eine niedrige Priorität also nur Arbeit, die sich wiederholen lässt.
* Preemption schaut nur auf den eigenen Node des Pods. Kubernetes verschiebt den vergrößerten Pod nicht auf einen größeren Node, denn dann wäre es kein „in place“ mehr.

## Außerdem neu: Arbeit im Inneren des Nodes

Zwei weitere Änderungen in v1.37 betreffen, wie der Node selbst CPU und Arbeitsspeicher verteilt. Im Browser kann man sie kaum beobachten, aber es ist gut, sie zu kennen:

* **Pod-Level-Resource-Manager** (Beta, standardmäßig aus). Seit v1.34 kannst du ein CPU- und Speicherbudget für den ganzen Pod festlegen (`spec.resources`) statt für jeden Container einzeln. Jetzt können auch der CPU-, Memory- und Topology-Manager des Kubelets dieses Budget nutzen. So bekommt zum Beispiel ein latenzkritischer Pod exklusive CPU-Kerne nahe an seinem Arbeitsspeicher, ohne dass jeder kleine Hilfscontainer im Pod eigene Kerne braucht.
* **Memory QoS** (Beta). Auf Linux-Nodes mit cgroup v2 sagt das Kubelet dem Kernel, wie viel Arbeitsspeicher jedem Container garantiert ist. Der Kernel schützt diesen Speicher dann davor, weggenommen zu werden. Setzt du zusätzlich die Kubelet-Option `memoryThrottlingFactor`, fängt der Kernel schon etwas vor dem Limit an, Speicher des Containers zurückzuholen. Das bremst den Container ein wenig, macht es aber unwahrscheinlicher, dass er abrupt mit einem Out-of-Memory-Fehler beendet wird.

## Zusammenfassung

* **Scale to Zero:** Mit `minReplicas: 0` kann der HPA einen Workload komplett herunterfahren, wenn es nichts zu tun gibt. Dafür braucht er eine `External`- oder `Object`-Metrik wie die Länge einer Queue, weil sich CPU an Pods, die es nicht gibt, nicht messen lässt.
* **Lass `replicas` im Deployment weg**, wenn ein HPA es verwaltet, sonst setzt jedes Apply die Anzahl der Pods zurück.
* **In-Place-Resize** ändert die CPU eines laufenden Pods ohne Neustart, über `kubectl patch --subresource resize`.
* **Preemption für Resize** (Alpha) schafft auf einem vollen Node Platz, indem Pods mit niedrigerer Priorität entfernt werden. Ist die Anforderung größer als der ganze Node, ist der Resize `Infeasible`.
