package main

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"log"
	"net/http"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
)

//go:embed index.html
var indexHTML embed.FS

//go:embed posts/*.md
var postsFS embed.FS

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
				html.WithUnsafe(),
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

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "text/html")
		if err := tmpl.Execute(w, map[string]interface{}{
			"Posts": posts,
		}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

	http.HandleFunc("/clicked", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<div class="bg-green-50 text-green-800 p-4 rounded-md border border-green-200 font-medium">Thanks for clicking! This content was loaded dynamically via HTMX without a full page reload.</div>`)
	})

	fmt.Println("Server listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
