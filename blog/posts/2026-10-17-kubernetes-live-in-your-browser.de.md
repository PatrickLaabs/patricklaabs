# Kubernetes, live im Browser

Probier Kubernetes selbst aus, ohne etwas zu installieren: Starte eine App, mach sie erreichbar, sieh zu, wie Kubernetes sie repariert, und lerne zwei häufige Fehler zu lesen. Alles läuft in diesem Browser-Tab.

## So funktionieren die Live-Beispiele

Über Kubernetes zu lesen bringt dich nur ein Stück weit. Viel schneller verstehst du es, wenn du Dinge ausprobierst und siehst, was passiert. Deshalb sind die Codeblöcke in diesem Post **live**. Sie laufen auf einem kleinen Kubernetes-Cluster, der mit [webernetes](https://github.com/ngrok/webernetes) direkt hier in deinem Browser simuliert wird. Es wird nichts an einen Server geschickt, und du kannst nichts kaputt machen.

Jeder Live-Block funktioniert gleich:

1. Klick auf **▶ kubectl apply**. Damit schickst du das Manifest (die YAML-Datei, die beschreibt, was du haben willst) an den simulierten Cluster, genau wie `kubectl apply -f` auf einem echten.
2. Unter dem Block siehst du, was Kubernetes angelegt hat und in welchem Zustand es ist.
3. Der Text im Block ist editierbar. Ändere etwas, klick erneut auf **▶ kubectl apply** und sieh dir an, was sich ändert. **↺ reset** löscht alles, was der Block angelegt hat, damit du von vorne anfangen kannst.

Jeder Block läuft in einem eigenen Namespace (einem abgetrennten Bereich im Cluster), der im Kopf des Blocks steht, zum Beispiel `namespace: live-1`. Den Namen brauchst du, wenn du das Terminal benutzt: Drück `~` auf deiner Tastatur, dann öffnet sich ein Terminal, das die wichtigsten `kubectl`-Befehle versteht.

## Schritt 1: Eine App starten und erreichbar machen

Wir fangen mit dem kleinsten Aufbau an, der wirklich etwas nützt: ein Webserver, an den du eine Anfrage schicken kannst. Dafür brauchen wir zwei Objekte:

* Ein **Pod** führt einen oder mehrere Container aus. Hier ist es ein einzelner Container mit dem Webserver nginx.
* Ein **Service** gibt dem Pod eine feste Adresse. Pods kommen und gehen, und ihre IP-Adressen ändern sich dabei. Der Service bleibt gleich und leitet Anfragen an alle Pods mit dem Label `app: web` weiter. Der Typ `NodePort` macht ihn von außerhalb des Clusters erreichbar.

Klick auf **▶ kubectl apply** und warte, bis der Pod `Running` anzeigt. Klick dann auf den Button **curl web**. Er schickt eine HTTP-Anfrage an den Service, und nginx antwortet.

```k8s-live
apiVersion: v1
kind: Pod
metadata:
  name: web
  labels:
    app: web
spec:
  containers:
    - name: nginx
      image: nginx:1.27
      ports:
        - containerPort: 80
---
apiVersion: v1
kind: Service
metadata:
  name: web
spec:
  type: NodePort
  selector:
    app: web
  ports:
    - port: 80
      targetPort: 80
```

Das Label verbindet die beiden: Der `selector` des Service sucht nach `app: web`, und der Pod trägt genau dieses Label. Probier es aus: Ändere das Label des Pods auf `app: other` und wende es erneut an. Der Service findet keinen Pod mehr, und curl meldet `no ready endpoints`.

## Schritt 2: Kubernetes die App reparieren lassen

Ein einzelner Pod hat ein Problem: Stürzt er ab oder wird er gelöscht, ist er weg. Niemand startet ihn neu. In der Praxis legt man Pods deshalb fast nie direkt an, sondern nutzt ein **Deployment**. Du sagst ihm, wie viele Kopien (Replicas) du haben willst, und Kubernetes sorgt dafür, dass jederzeit genau so viele laufen.

Wende den Block unten an. Es erscheinen drei Pods. Probier jetzt Folgendes:

1. Drück `~`, um das Terminal zu öffnen.
2. Lösch einen der Pods: `kubectl delete pod -n live-2 <name>`. Den Namen des Pods findest du in der Liste unter dem Block.
3. Beobachte die Liste: Kubernetes merkt, dass nur noch zwei Pods da sind, und startet sofort einen neuen.

Du kannst auch `replicas: 3` auf `5` ändern und erneut anwenden. Kubernetes startet dann zwei zusätzliche Pods.

```k8s-live
apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
spec:
  replicas: 3
  selector:
    matchLabels:
      app: web
  template:
    metadata:
      labels:
        app: web
    spec:
      containers:
        - name: nginx
          image: nginx:1.27
          ports:
            - containerPort: 80
```

Das ist die Grundidee hinter Kubernetes: Du beschreibst den Zustand, den du haben willst, und Kubernetes arbeitet ständig daran, ihn herzustellen.

## Schritt 3: CrashLoopBackOff verstehen

`CrashLoopBackOff` ist einer der Fehlerzustände, die du am häufigsten sehen wirst. Er bedeutet: Der Container stürzt immer wieder ab, und Kubernetes wartet vor jedem neuen Versuch etwas länger.

Der Container unten wurde genau dafür gebaut. Er startet, läuft in einen Fehler und beendet sich sofort. Wende ihn an und beobachte die Spalte `RESTARTS`. Der Zähler steigt, und die Pausen zwischen den Versuchen werden immer länger. Kubernetes macht das absichtlich, damit eine kaputte App nicht in einer Endlosschleife Ressourcen verbrennt.

Um herauszufinden, *warum* der Container abstürzt, schau dir seine Ausgabe an: Drück `~` und führ `kubectl logs -n live-3 crashy` aus. Das ist auch auf einem echten Cluster der erste Schritt.

```k8s-live
apiVersion: v1
kind: Pod
metadata:
  name: crashy
spec:
  containers:
    - name: app
      image: patricklaabs/crashloop:1.0
```

## Schritt 4: Den Fehler selbst finden

Jetzt bist du dran. Jemand hat diesen Pod am Freitagnachmittag noch schnell deployt, und er startet nicht. Wende ihn an, lies den Status unter dem Block und korrigiere den Fehler direkt im Block. Klick dann erneut auf **▶ kubectl apply**.

```k8s-live
apiVersion: v1
kind: Pod
metadata:
  name: friday-deploy
spec:
  containers:
    - name: nginx
      image: ngnix:1.27
```

Lösung: Das Image heißt `nginx`, nicht `ngnix`. Ein Image, das es nicht gibt, kann Kubernetes nicht herunterladen, deshalb landet der Pod in `ErrImagePull`. Ein kleiner Tippfehler, und auf einem echten Cluster würdest du genau denselben Fehler sehen.

Der Simulator kennt nur eine Handvoll Images, zum Beispiel `nginx`, `busybox` und die Beispiel-Apps aus diesen Posts. Jeder andere Image-Name endet in `ErrImagePull`.

## Zusammenfassung

* Ein **Pod** führt deinen Container aus. Ein **Service** macht ihn unter einer festen Adresse erreichbar und findet seine Pods über ihre Labels.
* Ein **Deployment** hält die gewünschte Anzahl an Pods am Laufen und ersetzt Pods, die verschwinden.
* **CrashLoopBackOff** heißt, dass dein Container immer wieder abstürzt. `kubectl logs` zeigt dir, warum.
* **ErrImagePull** bedeutet meistens, dass der Image-Name oder das Tag falsch ist.
