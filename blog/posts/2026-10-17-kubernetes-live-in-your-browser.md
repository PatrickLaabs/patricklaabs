# Kubernetes, live in your browser

Try Kubernetes yourself without installing anything: start an app, make it reachable, watch Kubernetes repair it and learn to read two common errors. Everything runs in this browser tab.

## How the live examples work

Reading about Kubernetes only gets you so far. You understand it much faster when you can try things and see what happens. That's why the code blocks in this post are **live**. They run on a small Kubernetes cluster that is simulated right here in your browser with [webernetes](https://github.com/ngrok/webernetes). Nothing is sent to a server, and you can't break anything.

Every live block works the same way:

1. Click **▶ kubectl apply**. This sends the manifest (the YAML file that describes what you want) to the simulated cluster, just like `kubectl apply -f` would on a real one.
2. Below the block you see what Kubernetes created and what state it is in.
3. The text in the block is editable. Change something, click **▶ kubectl apply** again and see what changes. **↺ reset** deletes everything the block created, so you can start from scratch.

Each block runs in its own namespace (a separate area of the cluster), which is shown in the block's header, for example `namespace: live-1`. You'll need that name when you use the terminal: press `~` on your keyboard to open a terminal that understands the most important `kubectl` commands.

## Step 1: Start an app and make it reachable

We start with the smallest setup that's actually useful: a web server that you can send a request to. For that we need two objects:

* A **Pod** runs one or more containers. Here it's a single container with the web server nginx.
* A **Service** gives the pod a stable address. Pods come and go, and their IP addresses change. The Service stays the same and forwards requests to every pod with the label `app: web`. The type `NodePort` makes it reachable from outside the cluster.

Click **▶ kubectl apply** and wait until the pod shows `Running`. Then click the **curl web** button. It sends an HTTP request to the Service, and nginx answers.

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

The label is what connects the two: the Service's `selector` searches for `app: web`, and the pod carries exactly that label. Try it: change the pod's label to `app: other` and apply again. The Service no longer finds a pod, and curl reports `no ready endpoints`.

## Step 2: Let Kubernetes repair the app

A single pod has a problem: if it crashes or gets deleted, it's gone. Nobody starts it again. In practice you therefore almost never create pods directly but use a **Deployment**. You tell it how many copies (replicas) you want, and Kubernetes makes sure that exactly this number is running at all times.

Apply the block below. Three pods appear. Now try it out:

1. Press `~` to open the terminal.
2. Delete one of the pods: `kubectl delete pod -n live-2 <name>`. You'll find the pod name in the list below the block.
3. Watch the list: Kubernetes notices that only two pods are left and immediately starts a new one.

You can also change `replicas: 3` to `5` and apply again. Kubernetes then starts two more pods.

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

This is the basic idea behind Kubernetes: you describe the state you want, and Kubernetes continuously works to get there.

## Step 3: Understand CrashLoopBackOff

`CrashLoopBackOff` is one of the error states you see most often. It means: the container keeps crashing, and Kubernetes waits a little longer before each new attempt.

The container below was built to show exactly that. It starts, runs into an error and stops right away. Apply it and watch the `RESTARTS` column. The counter goes up, and the pauses between attempts get longer and longer. Kubernetes does that on purpose, so that a broken app doesn't burn resources in an endless loop.

To find out *why* the container crashes, look at its output: press `~` and run `kubectl logs -n live-3 crashy`. That's also the first step on a real cluster.

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

## Step 4: Find the error yourself

Now it's your turn. Someone deployed this pod late on a Friday afternoon, and it doesn't start. Apply it, read the status below the block, and fix the error directly in the block. Then click **▶ kubectl apply** again.

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

Solution: the image is called `nginx`, not `ngnix`. Kubernetes can't download an image that doesn't exist, so the pod ends up in `ErrImagePull`. A small typo, and on a real cluster you'd see exactly the same error.

The simulator only knows a handful of images, such as `nginx`, `busybox` and the example apps used in these posts. Any other image name ends in `ErrImagePull`.

## Summary

* A **Pod** runs your container. A **Service** makes it reachable at a fixed address and finds its pods by their labels.
* A **Deployment** keeps the number of pods you asked for running and replaces pods that disappear.
* **CrashLoopBackOff** means your container keeps crashing. `kubectl logs` shows you why.
* **ErrImagePull** usually means that the image name or tag is wrong.
