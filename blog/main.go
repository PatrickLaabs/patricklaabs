package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"regexp"
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

// embedFS combines the embedded files into one tree laid out like the
// source directory, so production and dev mode share the same code paths.
type embedFS struct{}

func (embedFS) Open(name string) (fs.File, error) {
	switch {
	case name == "index.html":
		return indexHTML.Open(name)
	case name == "posts" || strings.HasPrefix(name, "posts/"):
		return postsFS.Open(name)
	case name == "static" || strings.HasPrefix(name, "static/"):
		return staticFS.Open(name)
	}
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

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

var md = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithParserOptions(
		parser.WithAutoHeadingID(),
	),
	goldmark.WithRendererOptions(
		html.WithHardWraps(),
	),
)

// loadPosts reads and renders every Markdown file in the posts directory of fsys.
func loadPosts(fsys fs.FS) []Post {
	var posts []Post
	entries, err := fs.ReadDir(fsys, "posts")
	if err != nil {
		// Log error but do not fatal in case there are no posts or directory is missing
		log.Println("Could not read posts directory:", err)
		return nil
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		data, err := fs.ReadFile(fsys, "posts/"+entry.Name())
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
	return posts
}

// contentVersion fingerprints the files a dev-mode page depends on, so the
// browser can tell when it needs to reload.
func contentVersion(fsys fs.FS) string {
	var latest time.Time
	var count int
	for _, root := range []string{"index.html", "posts", "static"} {
		fs.WalkDir(fsys, root, func(_ string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if info, err := d.Info(); err == nil {
				count++
				if info.ModTime().After(latest) {
					latest = info.ModTime()
				}
			}
			return nil
		})
	}
	return fmt.Sprintf("%d-%d", latest.UnixNano(), count)
}

// assetVersion fingerprints everything under static/. The page appends it to
// the URLs of files without a hash in their name (styles.css, htmx.min.js,
// js/main.js), so browsers and CDNs never mix a new page with old scripts.
func assetVersion(fsys fs.FS) string {
	h := sha256.New()
	fs.WalkDir(fsys, "static", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			return nil
		}
		fmt.Fprintf(h, "%s\x00%d\x00", path, len(data))
		h.Write(data)
		return nil
	})
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// hashedAsset matches bundler output like js/chaos-EYWIP36I.js, whose name
// changes whenever its content does.
var hashedAsset = regexp.MustCompile(`-[A-Z0-9]{8}\.js$`)

func main() {
	dev := flag.Bool("dev", false, "serve templates, posts and static files from disk and live-reload the browser on changes")
	addr := flag.String("addr", ":8080", "address to listen on")
	flag.Parse()

	// In dev mode everything is read from the working directory on every
	// request; otherwise the embedded copies are loaded once at startup.
	var contentFS fs.FS = embedFS{}
	if *dev {
		contentFS = os.DirFS(".")
	}

	loadTemplate := func() (*template.Template, error) {
		return template.ParseFS(contentFS, "index.html")
	}

	tmpl, err := loadTemplate()
	if err != nil {
		log.Fatal(err)
	}
	posts := loadPosts(contentFS)
	version := assetVersion(contentFS)

	static, err := fs.Sub(contentFS, "static")
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
		switch {
		case *dev:
			w.Header().Set("Cache-Control", "no-store")
		case hashedAsset.MatchString(r.URL.Path):
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		default:
			// Embedded files have no modification time, so give caches an
			// ETag to revalidate against instead.
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("ETag", `"`+version+`"`)
		}
		fileServer.ServeHTTP(w, r)
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}

		data := map[string]interface{}{
			"Posts":        posts,
			"AssetVersion": version,
		}
		t := tmpl
		if *dev {
			var err error
			if t, err = loadTemplate(); err != nil {
				log.Println("Error parsing template:", err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			data["Posts"] = loadPosts(contentFS)
			data["Dev"] = true
			data["Version"] = contentVersion(contentFS)
			data["AssetVersion"] = assetVersion(contentFS)
		}

		var buf bytes.Buffer
		if err := t.Execute(&buf, data); err != nil {
			log.Println("Error rendering template:", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
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

	if *dev {
		// Polled by the page via HTMX; tells it to reload once files change.
		mux.HandleFunc("/dev/reload", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			if r.URL.Query().Get("v") != contentVersion(contentFS) {
				w.Header().Set("HX-Refresh", "true")
			}
			w.WriteHeader(http.StatusNoContent)
		})
	}

	srv := &http.Server{
		Addr:              *addr,
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
	if *dev {
		fmt.Printf("Dev server listening on http://localhost%s (live reload enabled)\n", *addr)
	} else {
		fmt.Println("Server listening on", *addr)
	}

	select {
	case err := <-errCh:
		log.Fatal(err)
	case <-ctx.Done():
	}

	// Fail readiness first and give the endpoints controller time to remove
	// this pod from the Service before closing the listener.
	log.Println("Shutting down")
	ready.Store(false)
	if !*dev {
		time.Sleep(5 * time.Second)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
