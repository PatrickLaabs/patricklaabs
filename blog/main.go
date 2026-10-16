package main

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"strings"
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

	mux.HandleFunc("/clicked", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<div class="bg-green-50 text-green-800 p-4 rounded-md border border-green-200 font-medium">Thanks for clicking! This content was loaded dynamically via HTMX without a full page reload.</div>`)
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

	fmt.Println("Server listening on :8080")
	log.Fatal(srv.ListenAndServe())
}
