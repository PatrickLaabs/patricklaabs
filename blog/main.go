package main

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
)

//go:embed index.html
var indexHTML embed.FS

//go:embed posts/*.md
var postsFS embed.FS

//go:embed static
var staticFS embed.FS

// ready reports whether the server should receive traffic. It flips to false
// on SIGTERM so Kubernetes stops routing requests before we shut down.
var ready atomic.Bool

// probeHandler answers Kubernetes probe requests with a plain-text status.
func probeHandler(ok func() bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if !ok() {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintln(w, "not ready")
			return
		}
		fmt.Fprintln(w, "ok")
	}
}

// securityHeaders sets defensive response headers on every response.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}

type Post struct {
	Name    string
	Content template.HTML
}

func main() {
	// Parse the template
	tmpl, err := template.ParseFS(indexHTML, "index.html")
	if err != nil {
		log.Fatal(err)
	}

	// Read and parse markdown files
	var posts []Post
	entries, err := postsFS.ReadDir("posts")
	if err != nil {
		// Log error but do not fatal in case there are no posts or directory is missing
		log.Println("Could not read posts directory:", err)
	} else {
		md := goldmark.New(
			goldmark.WithExtensions(extension.GFM),
			goldmark.WithParserOptions(
				parser.WithAutoHeadingID(),
			),
			goldmark.WithRendererOptions(
				html.WithHardWraps(),
			),
		)

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			data, err := postsFS.ReadFile("posts/" + entry.Name())
			if err != nil {
				log.Println("Error reading post:", err)
				continue
			}

			var buf bytes.Buffer
			if err := md.Convert(data, &buf); err != nil {
				log.Println("Error converting post:", err)
				continue
			}
			posts = append(posts, Post{
				Name:    entry.Name(),
				Content: template.HTML(buf.String()),
			})
		}
	}

	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	fileServer := http.StripPrefix("/static/", http.FileServerFS(static))
	mux.HandleFunc("/static/", func(w http.ResponseWriter, r *http.Request) {
		// Serve files only; no directory listings.
		if strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		fileServer.ServeHTTP(w, r)
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}

		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, map[string]interface{}{
			"Posts": posts,
		}); err != nil {
			log.Println("Error rendering template:", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		buf.WriteTo(w)
	})

	// Liveness: the process is up and able to serve requests.
	mux.HandleFunc("/healthz", probeHandler(func() bool { return true }))
	// Readiness: templates and posts are loaded and we are not shutting down.
	mux.HandleFunc("/readyz", probeHandler(ready.Load))

	mux.HandleFunc("/clicked", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<div class="rounded-lg border border-emerald-500/30 bg-cluster-950 p-4 font-mono text-sm"><p class="text-emerald-300">deployment.apps/blog configured</p><p class="mt-1 text-slate-500"># Loaded dynamically via HTMX &mdash; no full page reload.</p></div>`)
	})

	srv := &http.Server{
		Addr:              ":8080",
		Handler:           securityHeaders(mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 16,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	ready.Store(true)
	fmt.Println("Server listening on :8080")

	select {
	case err := <-errCh:
		log.Fatal(err)
	case <-ctx.Done():
	}

	// Fail readiness first and give the endpoints controller time to remove
	// this pod from the Service before closing the listener.
	log.Println("Shutting down")
	ready.Store(false)
	time.Sleep(5 * time.Second)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
