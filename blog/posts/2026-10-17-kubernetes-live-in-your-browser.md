# Kubernetes, live in your browser

Reading about Kubernetes is fine. Poking at it is better. The manifests in this post are **live**: hit **▶ kubectl apply** and they run on a simulated cluster right here in your browser tab, powered by [webernetes](https://github.com/ngrok/webernetes). Every block is editable, so change things and apply again. Each one runs in its own namespace, shown in its header.

## A pod and a service

The smallest useful thing: one nginx pod and a `NodePort` service in front of it. Apply it, wait for `Running`, then hit the **curl** button.

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

## Deployments heal themselves

A bare pod that dies stays dead. A `Deployment` keeps the number of replicas you asked for. Apply this, then press `~` and delete one of its pods with `kubectl delete pod -n <namespace> <name>`. Or change `replicas` to `5` and apply again.

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

## CrashLoopBackOff, up close

This container starts, panics and exits with code 1. Watch the kubelet restart it, then back off for longer and longer between attempts. `kubectl logs -n <namespace> crashy` in the terminal tells you why.

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

## Spot the typo

Classic Friday afternoon deploy. Apply it, read the status, fix the image name and apply again.

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

The simulator knows the images `nginx`, `busybox`, `patricklaabs/blog` and `patricklaabs/crashloop`. Anything else ends in `ErrImagePull`, just like a typo would on a real cluster.
