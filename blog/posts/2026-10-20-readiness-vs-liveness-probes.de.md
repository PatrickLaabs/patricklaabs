# Readiness- vs. Liveness-Probes, live

Kubernetes erkennt, ob dein Container läuft, aber nicht, ob deine App wirklich funktioniert. Probes schließen diese Lücke. In diesem Post siehst du Schritt für Schritt und live im Browser, welche Probe du wann brauchst und was ohne sie schiefgeht.

## Das Problem: Laufen ist nicht dasselbe wie Funktionieren

Kubernetes startet deinen Container und sieht, dass der Prozess läuft. Ob deine App darin auch wirklich Anfragen beantworten kann, weiß Kubernetes nicht. Vielleicht lädt sie noch Daten, vielleicht hängt sie fest. Von außen sieht das alles gleich aus: Der Prozess ist da.

**Probes** schließen diese Lücke. Eine Probe ist eine kleine Prüfung, meistens eine HTTP-Anfrage an eine Adresse in deiner App wie `/healthz`, die das **Kubelet** (der Kubernetes-Agent auf jedem Node, der Container startet und überwacht) in regelmäßigen Abständen ausführt. Je nach Antwort reagiert Kubernetes. Die beiden häufigsten Probes stellen zwei sehr unterschiedliche Fragen:

* **Readiness-Probe:** „Kann dieser Pod gerade Anfragen bearbeiten?“ Wenn nicht, schickt Kubernetes ihm keinen Traffic mehr. Neu gestartet wird nichts. Sobald die Probe wieder erfolgreich ist, bekommt der Pod wieder Traffic.
* **Liveness-Probe:** „Ist dieser Container nicht mehr zu retten?“ Wenn ja, beendet das Kubelet den Container und startet einen neuen.

Die beiden zu verwechseln ist einer der Klassiker, um eine eigentlich gesunde App lahmzulegen. Deshalb schauen wir uns unten jeden Fall einzeln an.

Wie im [letzten Post](/de/posts/kubernetes-live-in-your-browser) läuft jedes Manifest auf einem simulierten Cluster in deinem Browser. Klick auf **▶ kubectl apply**, um es zu starten, ändere den Text und wende ihn erneut an, um Varianten auszuprobieren. Mit `~` öffnest du ein Terminal für `kubectl`-Befehle. Jeder Block läuft in einem eigenen Namespace, der in seinem Kopf steht.

## Schritt 1: Ohne Readiness-Probe bekommen Nutzer Fehler

Unsere Beispiel-App `patricklaabs/slowstart` ist ein kleiner Shop. Nach dem Start braucht sie etwa acht Sekunden, um ihre Daten zu laden (ihren Cache aufzuwärmen). Bis dahin beantwortet sie jede Anfrage mit dem Fehler `503 Service Unavailable`.

Das Manifest unten startet zwei Kopien des Shops (ein **Deployment** mit zwei Replicas) und einen **Service**, der Anfragen auf sie verteilt. Eine Readiness-Probe gibt es nicht. Kubernetes hält einen Pod deshalb für bereit, sobald sein Prozess startet, und der Service schickt ihm sofort Anfragen.

Wende den Block an und klick direkt nach dem Start der Pods ein paarmal auf den Button **curl shop**.

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

In den ersten Sekunden bekommst du `503`. Genau das würden auch deine Nutzer sehen. Auf einem echten Cluster passiert das nicht nur einmal: Es passiert bei jedem Rollout, bei jedem Hochskalieren und jedes Mal, wenn ein Node für Wartungsarbeiten geleert wird, denn irgendwo starten immer neue Pods.

## Schritt 2: Mit Readiness-Probe warten Pods, bis sie bereit sind

Jetzt sagen wir Kubernetes, wie es prüfen kann, ob der Shop bereit ist. Die App bietet dafür die Adresse `/readyz` an. Sie antwortet mit `ok`, sobald der Cache warm ist. Mit dem neuen Block `readinessProbe` fragt das Kubelet diese Adresse jede Sekunde ab (`periodSeconds: 1`). Ein Pod bekommt erst dann Anfragen vom Service, wenn die Prüfung erfolgreich ist.

Wende den Block an und beobachte die Spalte `READY`. Sie zeigt ein paar Sekunden lang `0/1` und springt erst dann auf `1/1`.

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

Klickst du in den ersten Sekunden auf curl, bekommst du jetzt `no ready endpoints` statt einer `503`. Das heißt, der Service kennt keinen einzigen Pod, an den er Anfragen schicken darf. Das sieht auf den ersten Blick schlimmer aus, ist aber das richtige Verhalten: In dieser Demo starten alle Pods gleichzeitig. Auf einem echten Cluster starten neue Pods, während die alten noch laufen. Weil die neuen erst Traffic bekommen, wenn sie bereit sind, gehen die Anfragen in der Zwischenzeit weiter an die alten, funktionierenden Pods. Deine Nutzer merken davon nichts.

Genau diesen Trick nutzt übrigens auch dieser Blog. Wenn Kubernetes einen Pod beenden will, schickt es ihm zuerst das Signal `SIGTERM`. Der Blog meldet sich daraufhin als nicht bereit (`503` auf seiner Readiness-Adresse), sodass der Service ihm keine Anfragen mehr schickt, bevor er herunterfährt. Deshalb kann der [Chaos Monkey](/de/#chaos) auf der Startseite Pods beenden, ohne dass Besucher Fehler sehen.

## Schritt 3: Eine Liveness-Probe startet eine hängende App neu

Jetzt zur anderen Probe. Unsere zweite Beispiel-App, `patricklaabs/deadlock`, funktioniert etwa fünfzehn Sekunden lang einwandfrei. Dann hängt sie fest: Sie wartet auf eine Sperre, die nie freigegeben wird (ein sogenannter Deadlock). Der Prozess stürzt nicht ab, er arbeitet einfach nicht mehr und beantwortet jede Anfrage mit `500`.

Ohne Liveness-Probe würde Kubernetes das nicht bemerken. Der Pod würde für immer `Running` anzeigen und dabei nur Fehler ausliefern. Die `livenessProbe` unten fragt alle zwei Sekunden die Adresse `/healthz` ab. Nach drei fehlgeschlagenen Prüfungen in Folge (`failureThreshold: 3`) startet das Kubelet den Container neu.

Wende den Block an, klick ab und zu auf **curl stuck** und behalte die Spalte `RESTARTS` im Auge. Nach jedem Neustart funktioniert die App wieder fünfzehn Sekunden lang, bis zum nächsten Deadlock.

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

Zum Vergleich: Lösch den Block `livenessProbe` und wende das Manifest erneut an. Der Pod bleibt bei `Running` und `1/1`, sieht völlig gesund aus und antwortet trotzdem nur mit Fehlern.

Ein Neustart ist natürlich keine echte Lösung. Der Fehler in der App muss trotzdem behoben werden. Bis dahin sorgt die Liveness-Probe aber dafür, dass die App die meiste Zeit benutzbar bleibt.

## Schritt 4: Den Fehler selbst finden

Jemand hat dem langsam startenden Shop aus Schritt 1 „sicherheitshalber“ eine Liveness-Probe verpasst. Wende den Block an und beobachte den Restart-Zähler. Drück dann `~` und führ `kubectl logs -n live-4 too-eager` aus, um zu sehen, was die App gerade gemacht hat, als sie beendet wurde.

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

Die Logs zeigen, wie die App ihren Cache aufwärmt und die Probe mit `503` beantwortet, aber nie die Zeile `Cache warm, ready to serve traffic`. Das passiert hier: Die App braucht acht Sekunden zum Starten. Die Probe prüft alle zwei Sekunden und gibt nach drei Fehlschlägen auf, also nach etwa sechs Sekunden. Das Kubelet beendet den Container kurz bevor er bereit gewesen wäre. Der neue Container fängt wieder von vorne an zu laden, wird wieder beendet, und so weiter. Am Ende landet der Pod in `CrashLoopBackOff`. Eine gesunde App, zu Tode neu gestartet.

Die Lösung ist eine dritte Probe, die **Startup-Probe**. Solange sie noch nicht erfolgreich war, pausiert Kubernetes die Liveness- und Readiness-Probes. Damit bekommt ein langsamer Start sein eigenes, großzügigeres Zeitbudget. Füg diesen Block unter der `livenessProbe` ein (mit derselben Einrückung) und wende erneut an:

```yaml
      startupProbe:
        httpGet:
          path: /healthz
          port: 8080
        periodSeconds: 1
        failureThreshold: 30
```

Damit hat die App bis zu 30 Sekunden (30 Versuche, einer pro Sekunde) Zeit für ihre erste erfolgreiche Prüfung. Danach übernimmt die strenge Liveness-Probe.

## Zusammenfassung

* **Readiness** entscheidet, ob ein Pod Traffic bekommt. Nutze sie für jeden Container, der Anfragen beantwortet.
* **Liveness** entscheidet, ob ein Container neu gestartet wird. Nutze sie nur für Fehler, die ein Neustart wirklich behebt, wie einen Deadlock. Lass sie nie eine Datenbank oder einen anderen Service prüfen: Fällt diese Abhängigkeit aus, starten alle Pods gleichzeitig neu, und aus einem Ausfall werden zwei.
* **Startup** schützt langsame Starts. Nutze sie statt eines sehr großen `initialDelaySeconds`.
* Halte die Prüfadressen billig. Eine Probe läuft alle paar Sekunden auf jedem Pod, so lange der Pod existiert.
