# Readiness- vs. Liveness-Probes, live

Kubernetes weiß, ob dein Container *läuft*. Ob deine App tatsächlich *funktioniert*, weiß es nicht. Probes schließen diese Lücke, und die zwei gängigsten beantworten zwei sehr unterschiedliche Fragen:

* **Readiness:** „Soll dieser Pod gerade Traffic bekommen?“ Wenn nicht, wird er aus dem Service genommen, aber nichts wird neu gestartet.
* **Liveness:** „Ist dieser Container hoffnungslos kaputt?“ Wenn ja, beendet das kubelet ihn und startet einen frischen.

Sie zu verwechseln ist einer der Klassiker, um eine völlig gesunde App lahmzulegen. Wie im [letzten Post](/de/posts/kubernetes-live-in-your-browser) läuft jedes Manifest unten auf einem simulierten Cluster in deinem Browser: Klick auf **▶ kubectl apply**, ändere etwas, wende es erneut an.

## Ohne Readiness-Probe: Traffic zu früh

`patricklaabs/slowstart` braucht etwa acht Sekunden, um seinen Cache aufzuwärmen, bevor es echte Requests bedienen kann. Bis dahin antwortet es mit `503`. Unten gibt es keine Readiness-Probe, also markiert Kubernetes den Pod als `Ready`, sobald der Prozess startet, und der Service schickt ihm fröhlich Traffic. Wende es an und hämmer auf den **curl**-Button.

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

Jeder Nutzer, der während des Aufwärmens auf einem Pod landet, bekommt einen Fehler. Auf einem echten Cluster passiert das bei jedem Rollout, jedem Hochskalieren und jedem Node-Drain.

## Mit Readiness-Probe: warten, bis er warm ist

Dieselbe App, ein Block mehr. Das kubelet fragt jetzt jede Sekunde `/readyz`, und der Pod kommt erst in den Service, wenn er `ok` sagt. Sieh zu, wie die Spalte `READY` ein paar Sekunden auf `0/1` bleibt, während curl überhaupt keine Endpoints findet, und danach klappt jeder Request.

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

„Keine Endpoints“ klingt schlimmer als ein `503`, aber mit mehr als einer Replica heißt das: Der Traffic geht an die Pods, die bereit *sind*. Genau das verhindert, dass der [Chaos Monkey](/de/#chaos) auf der Startseite Fehler verursacht: Dieser Blog schaltet seine eigene Readiness auf `503`, sobald er `SIGTERM` bekommt, damit der Service ihm keine Requests mehr schickt, bevor er herunterfährt.

## Liveness: wenn der Prozess lebt, aber die App tot ist

`patricklaabs/deadlock` läuft etwa fünfzehn Sekunden lang einwandfrei und hängt dann fest, weil es auf einen Lock wartet, der nie freigegeben wird. Der Prozess stürzt nicht ab, also würde Kubernetes ihn ohne Probe ewig behalten, und er würde allen mit `500` antworten. Die Liveness-Probe unten bemerkt das nach drei fehlgeschlagenen Checks und lässt das kubelet den Container neu starten. Behalte die Spalte `RESTARTS` im Auge: Jeder Neustart bringt wieder fünfzehn gute Sekunden, bis zum nächsten Deadlock.

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

Lösch den Block `livenessProbe` und wende es erneut an, um die Alternative zu sehen: ein Pod, der `Running` und `1/1` meldet, während er nur Fehler ausliefert.

## Finde den Fehler

Jemand hat der langsam startenden App „zur Sicherheit“ eine Liveness-Probe verpasst. Wende es an und beobachte den Restart-Zähler. Drück dann `~` und führ `kubectl describe pod -n <namespace>` aus, um zu sehen, warum.

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

Die App braucht acht Sekunden zum Aufwärmen, aber die Probe gibt nach drei Fehlschlägen im Abstand von zwei Sekunden auf. Das kubelet beendet den Container kurz bevor er bereit gewesen wäre, er wärmt sich wieder auf, wird wieder beendet und landet in `CrashLoopBackOff`. Eine gesunde App, zu Tode neu gestartet.

Die Lösung ist eine **Startup-Probe**. Solange sie läuft, pausieren Liveness- und Readiness-Checks, und ein langsamer Start bekommt sein eigenes, großzügigeres Budget. Füg das neben der Liveness-Probe ein und wende es erneut an:

```yaml
      startupProbe:
        httpGet:
          path: /healthz
          port: 8080
        periodSeconds: 1
        failureThreshold: 30
```

Damit hat der erste erfolgreiche Check bis zu 30 Sekunden Zeit, danach übernimmt die strenge Liveness-Probe.

## Faustregeln

* **Readiness** entscheidet über Traffic. Nutze sie für alles, was Requests bedient.
* **Liveness** entscheidet über Neustarts. Nutze sie nur für Fehler, die ein Neustart wirklich behebt, etwa einen Deadlock. Lass sie nie eine Datenbank oder einen anderen Service prüfen: Fällt diese Abhängigkeit aus, starten alle Pods gleichzeitig neu, und aus einem Ausfall werden zwei.
* **Startup** schützt langsame Starts. Greif zu ihr statt zu einem riesigen `initialDelaySeconds`.
* Halte die Endpoints billig. Eine Probe läuft alle paar Sekunden auf jedem Pod, für immer.
