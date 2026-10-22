# Kubernetes, live im Browser

Über Kubernetes zu lesen ist gut. Selbst daran herumzuspielen ist besser. Die Manifeste in diesem Post sind **live**: Klick auf **▶ kubectl apply**, und sie laufen auf einem simulierten Cluster direkt hier in deinem Browser-Tab, angetrieben von [webernetes](https://github.com/ngrok/webernetes). Jeder Block ist editierbar, also ändere etwas und wende ihn erneut an. Jeder läuft in seinem eigenen Namespace, der in seinem Kopf angezeigt wird.

## Ein Pod und ein Service

Das Kleinste, was sinnvoll ist: ein nginx-Pod und ein `NodePort`-Service davor. Wende es an, warte auf `Running` und klick dann auf den **curl**-Button.

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

## Deployments heilen sich selbst

Ein nackter Pod, der stirbt, bleibt tot. Ein `Deployment` hält die Zahl der Replicas, die du verlangt hast. Wende es an, drück dann `~` und lösch einen seiner Pods mit `kubectl delete pod -n <namespace> <name>`. Oder setz `replicas` auf `5` und wende es erneut an.

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

## CrashLoopBackOff aus der Nähe

Dieser Container startet, gerät in Panik und beendet sich mit Code 1. Sieh zu, wie das kubelet ihn neu startet und zwischen den Versuchen immer länger wartet. `kubectl logs -n <namespace> crashy` im Terminal verrät dir, warum.

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

## Finde den Tippfehler

Klassisches Deployment am Freitagnachmittag. Wende es an, lies den Status, korrigiere den Image-Namen und wende es erneut an.

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

Der Simulator kennt die Images `nginx`, `busybox`, `patricklaabs/blog` und `patricklaabs/crashloop`. Alles andere endet in `ErrImagePull`, genau wie ein Tippfehler auf einem echten Cluster.
