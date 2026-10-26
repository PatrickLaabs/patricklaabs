# Hello, World!

Welcome to my blog. Here I write about Kubernetes and cloud topics, explained step by step so that you can follow along even if you are just getting started.

## What you'll find here

Most posts are hands-on. Instead of only describing how something works, they contain **live examples**: Kubernetes manifests that you can run on a simulated cluster directly in your browser. You click a button, watch what happens, change something and try again. You don't need to install anything, and you can't break anything.

Every post explains three things:

* **What** we are doing.
* **Why** it matters in practice.
* **What you should see** when you try it yourself.

If you want to jump right in, start with [Kubernetes, live in your browser](/posts/kubernetes-live-in-your-browser).

## Under the hood

The blog itself is a small Go server that renders posts written in Markdown and uses HTMX for the interactive parts. It runs on a Kubernetes cluster, of course.

```go
fmt.Println("HTMX + Go = Awesome")
```
