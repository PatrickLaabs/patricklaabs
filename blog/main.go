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
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/klauspost/compress/gzhttp"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
)

//go:embed index.html
var indexHTML embed.FS

//go:embed posts/*.md
var postsFS embed.FS

//go:embed about*.md
var aboutFS embed.FS

//go:embed static
var staticFS embed.FS

// embedFS combines the embedded files into one tree laid out like the
// source directory, so production and dev mode share the same code paths.
type embedFS struct{}

func (embedFS) Open(name string) (fs.File, error) {
	switch {
	case name == "index.html":
		return indexHTML.Open(name)
	case strings.HasPrefix(name, "about") && strings.HasSuffix(name, ".md"):
		return aboutFS.Open(name)
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
	Slug        string
	Lang        string
	Title       string
	Summary     string // first paragraph, shortened for the post list
	Description string // first paragraph, shortened for search results
	Date        time.Time
	Content     template.HTML
}

// Page is a standalone Markdown page outside the post list, like /about.
type Page struct {
	Slug        string
	Lang        string
	Title       string
	Summary     string
	Description string
	Content     template.HTML
}

// YearGroup is a slice of posts published in the same year, for the archive
// on the front page.
type YearGroup struct {
	Year  int
	Posts []Post
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

// postFilename matches posts/YYYY-MM-DD-slug.md and its translations like
// posts/YYYY-MM-DD-slug.de.md; the date is the publish date. Translations of
// a post share its slug.
var postFilename = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})-([a-z0-9-]+)(?:\.([a-z]{2}))?\.md$`)

// Library holds every post by slug, then by language code.
type Library map[string]map[string]Post

// pick returns the version of the post slug in lang, or else the one in the
// first language that has it.
func (lib Library) pick(slug, lang string) (Post, bool) {
	versions := lib[slug]
	if p, ok := versions[lang]; ok {
		return p, true
	}
	for _, l := range languages {
		if p, ok := versions[l.Code]; ok {
			return p, true
		}
	}
	return Post{}, false
}

// list returns one version of every post for lang, newest first. Posts that
// are not translated into lang are listed in their own language.
func (lib Library) list(lang string) []Post {
	posts := make([]Post, 0, len(lib))
	for slug := range lib {
		p, _ := lib.pick(slug, lang)
		posts = append(posts, p)
	}
	sort.Slice(posts, func(i, j int) bool {
		if !posts[i].Date.Equal(posts[j].Date) {
			return posts[i].Date.After(posts[j].Date)
		}
		return posts[i].Slug < posts[j].Slug
	})
	return posts
}

// loadPosts reads and renders every Markdown file in the posts directory of
// fsys.
func loadPosts(fsys fs.FS) Library {
	lib := Library{}
	entries, err := fs.ReadDir(fsys, "posts")
	if err != nil {
		// Log error but do not fatal in case there are no posts or directory is missing
		log.Println("Could not read posts directory:", err)
		return lib
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		m := postFilename.FindStringSubmatch(entry.Name())
		if m == nil {
			log.Printf("Skipping post %s: name must look like YYYY-MM-DD-slug.md", entry.Name())
			continue
		}
		date, err := time.Parse(time.DateOnly, m[1])
		if err != nil {
			log.Printf("Skipping post %s: %v", entry.Name(), err)
			continue
		}
		slug, lang := m[2], m[3]
		if lang == "" {
			lang = defaultLanguage().Code
		} else if _, ok := languageByCode(lang); !ok {
			log.Printf("Skipping post %s: unknown language %q", entry.Name(), lang)
			continue
		}
		if _, taken := lib[slug][lang]; taken {
			log.Printf("Skipping post %s: slug %q is already taken in %q", entry.Name(), slug, lang)
			continue
		}

		data, err := fs.ReadFile(fsys, "posts/"+entry.Name())
		if err != nil {
			log.Println("Error reading post:", err)
			continue
		}

		doc := md.Parser().Parse(text.NewReader(data))
		title, intro := postInfo(doc, data)
		if title == "" {
			title = slug
		}
		var buf bytes.Buffer
		if err := md.Renderer().Render(&buf, data, doc); err != nil {
			log.Println("Error converting post:", err)
			continue
		}
		if lib[slug] == nil {
			lib[slug] = map[string]Post{}
		}
		lib[slug][lang] = Post{
			Slug:        slug,
			Lang:        lang,
			Title:       title,
			Summary:     truncate(intro, 220),
			Description: description(intro),
			Date:        date,
			Content:     template.HTML(buf.String()),
		}
	}
	return lib
}

// loadPage reads and renders the page served at /slug: slug.md in the
// default language and, like posts, slug.<lang>.md for each translation.
// Only the default language is required.
func loadPage(fsys fs.FS, slug string) (map[string]Page, error) {
	pages := map[string]Page{}
	for i, l := range languages {
		name := slug + ".md"
		if i > 0 {
			name = slug + "." + l.Code + ".md"
		}
		p, err := loadPageFile(fsys, name, slug)
		if i > 0 && errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		p.Lang = l.Code
		pages[l.Code] = p
	}
	return pages, nil
}

func loadPageFile(fsys fs.FS, name, slug string) (Page, error) {
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		return Page{}, err
	}
	doc := md.Parser().Parse(text.NewReader(data))
	title, intro := postInfo(doc, data)
	var buf bytes.Buffer
	if err := md.Renderer().Render(&buf, data, doc); err != nil {
		return Page{}, err
	}
	return Page{Slug: slug, Title: title, Summary: truncate(intro, 220), Description: description(intro), Content: template.HTML(buf.String())}, nil
}

// postInfo returns the text of a post's first top-level heading and of the
// first paragraph after it.
func postInfo(doc ast.Node, source []byte) (title, intro string) {
	for n := doc.FirstChild(); n != nil; n = n.NextSibling() {
		switch n := n.(type) {
		case *ast.Heading:
			if title == "" && n.Level == 1 {
				title = plainText(n, source)
			}
		case *ast.Paragraph:
			if intro == "" {
				intro = plainText(n, source)
			}
		}
		if title != "" && intro != "" {
			break
		}
	}
	return title, intro
}

// plainText concatenates the text inside n without any Markdown markup.
func plainText(n ast.Node, source []byte) string {
	var b strings.Builder
	ast.Walk(n, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.Text:
			b.Write(n.Segment.Value(source))
			if n.SoftLineBreak() || n.HardLineBreak() {
				b.WriteByte(' ')
			}
		case *ast.String:
			b.Write(n.Value)
		}
		return ast.WalkContinue, nil
	})
	return strings.Join(strings.Fields(b.String()), " ")
}

// truncate shortens s to at most max bytes, cutting at a word boundary.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	s = s[:max]
	if i := strings.LastIndexByte(s, ' '); i > 0 {
		s = s[:i]
	}
	return strings.TrimRight(s, " ,.;:-") + "…"
}

// groupByYear splits posts, which are sorted newest first, by publish year.
func groupByYear(posts []Post) []YearGroup {
	var groups []YearGroup
	for _, p := range posts {
		if len(groups) == 0 || groups[len(groups)-1].Year != p.Date.Year() {
			groups = append(groups, YearGroup{Year: p.Date.Year()})
		}
		g := &groups[len(groups)-1]
		g.Posts = append(g.Posts, p)
	}
	return groups
}

// contentVersion fingerprints the files a dev-mode page depends on, so the
// browser can tell when it needs to reload.
func contentVersion(fsys fs.FS) string {
	var latest time.Time
	var count int
	for _, root := range []string{"index.html", "about.md", "about.de.md", "posts", "static"} {
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

// origin returns the scheme and host the request was made to, for absolute
// URLs in dev mode, where the blog runs on localhost.
func origin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// envOr returns the environment variable key, or fallback when it is unset.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// hashedAsset matches bundler output like js/chaos-EYWIP36I.js, whose name
// changes whenever its content does.
var hashedAsset = regexp.MustCompile(`-[A-Z0-9]{8}\.js$`)

func main() {
	dev := flag.Bool("dev", false, "serve templates, posts and static files from disk and live-reload the browser on changes")
	addr := flag.String("addr", ":8080", "address to listen on")
	viewsEvery := flag.Duration("views-log-interval", time.Hour, "how often to log a summary of the page views")
	baseURL := flag.String("base-url", envOr("BLOG_BASE_URL", defaultBaseURL), "scheme and host the blog is published at, for absolute URLs; dev mode uses the request's instead")
	valkeyAddr := flag.String("valkey", os.Getenv("BLOG_VALKEY_ADDR"), "host:port of a Valkey server to add up page views in; empty keeps them in memory only")
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
	about, err := loadPage(contentFS, "about")
	if err != nil {
		log.Fatal(err)
	}
	version := assetVersion(contentFS)

	views := newViews(*valkeyAddr)
	defer views.Close()

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

	// base returns the scheme and host for absolute URLs.
	base := func(r *http.Request) string {
		if *dev {
			return origin(r)
		}
		return strings.TrimRight(*baseURL, "/")
	}

	// Files browsers and crawlers expect at the root of the site.
	serveStatic := func(name string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/static/" + name
			mux.ServeHTTP(w, r2)
		}
	}
	mux.HandleFunc("GET /favicon.ico", serveStatic("favicon.ico"))
	mux.HandleFunc("GET /apple-touch-icon.png", serveStatic("apple-touch-icon.png"))

	mux.HandleFunc("GET /robots.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		io.WriteString(w, robotsTxt(base(r)))
	})

	// render executes the page template with data. In dev mode the template
	// and posts are reloaded from disk first, so data may be built from them.
	// A "Status" in data replaces the default 200 OK.
	render := func(w http.ResponseWriter, r *http.Request, t *template.Template, data map[string]any) {
		data["AssetVersion"] = version
		if *dev {
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
		if status, ok := data["Status"].(int); ok {
			w.WriteHeader(status)
			buf.WriteTo(w)
			return
		}
		buf.WriteTo(w)
		views.Count(r)
	}

	// current returns the template and posts to serve: the ones loaded at
	// startup, or fresh copies from disk in dev mode.
	current := func(w http.ResponseWriter) (*template.Template, Library, bool) {
		if !*dev {
			return tmpl, posts, true
		}
		t, err := loadTemplate()
		if err != nil {
			log.Println("Error parsing template:", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return nil, nil, false
		}
		return t, loadPosts(contentFS), true
	}

	// pageData returns the template data shared by every page in lang at
	// path, which has no language prefix. has reports whether a language has
	// its own version of the page; the others get no hreflang link.
	pageData := func(r *http.Request, lang Language, path string, has func(Language) bool) map[string]any {
		var alts []Alternate
		for _, l := range languages {
			alts = append(alts, Alternate{
				Language: l,
				URL:      l.Prefix + path,
				Current:  l.Code == lang.Code,
				Exists:   has(l),
			})
		}
		return map[string]any{
			"Lang":       lang,
			"T":          lang.Text,
			"Origin":     base(r),
			"Canonical":  lang.Prefix + path,
			"Alternates": alts,
		}
	}
	always := func(Language) bool { return true }

	// notFound shows the 404 page in the language of the requested path.
	notFound := func(w http.ResponseWriter, r *http.Request) {
		t, _, ok := current(w)
		if !ok {
			return
		}
		lang := languageOfPath(r.URL.Path)
		data := pageData(r, lang, "/", func(Language) bool { return false })
		delete(data, "Canonical")
		data["Title"] = lang.Text.NotFoundTitle
		data["Description"] = lang.Text.NotFoundText
		data["NotFound"] = true
		data["NoIndex"] = true
		data["Status"] = http.StatusNotFound
		render(w, r, t, data)
	}
	mux.HandleFunc("/", notFound)

	mux.HandleFunc("GET /sitemap.xml", func(w http.ResponseWriter, r *http.Request) {
		_, lib, ok := current(w)
		if !ok {
			return
		}
		var buf bytes.Buffer
		if err := writeSitemap(&buf, base(r), lib); err != nil {
			log.Println("Error writing sitemap:", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		buf.WriteTo(w)
	})

	for _, lang := range languages {
		mux.HandleFunc(lang.Prefix+"/{$}", func(w http.ResponseWriter, r *http.Request) {
			t, lib, ok := current(w)
			if !ok {
				return
			}
			posts := lib.list(lang.Code)
			data := pageData(r, lang, "/", always)
			data["Home"] = true
			data["Posts"] = posts
			data["JSONLD"] = siteJSONLD(base(r), lang)
			render(w, r, t, data)
		})

		mux.HandleFunc("GET "+lang.Prefix+"/posts", func(w http.ResponseWriter, r *http.Request) {
			t, lib, ok := current(w)
			if !ok {
				return
			}
			posts := lib.list(lang.Code)
			data := pageData(r, lang, "/posts", always)
			data["Title"] = lang.Text.PostsTitle
			data["Description"] = lang.Text.PostsDescription
			data["Posts"] = posts
			data["Years"] = groupByYear(posts)
			render(w, r, t, data)
		})

		mux.HandleFunc("GET "+lang.Prefix+"/playground", func(w http.ResponseWriter, r *http.Request) {
			t, _, ok := current(w)
			if !ok {
				return
			}
			data := pageData(r, lang, "/playground", always)
			data["Title"] = lang.Text.PlaygroundTitle
			data["Description"] = lang.Text.PlaygroundDescription
			data["Playground"] = true
			render(w, r, t, data)
		})

		mux.HandleFunc("GET "+lang.Prefix+"/posts/{slug}", func(w http.ResponseWriter, r *http.Request) {
			t, lib, ok := current(w)
			if !ok {
				return
			}
			slug := r.PathValue("slug")
			p, ok := lib.pick(slug, lang.Code)
			if !ok {
				notFound(w, r)
				return
			}
			data := pageData(r, lang, "/posts/"+slug, func(l Language) bool {
				_, ok := lib[slug][l.Code]
				return ok
			})
			data["Post"] = p
			data["JSONLD"] = postJSONLD(base(r), base(r)+lang.Prefix+"/posts/"+slug, p)
			if p.Lang != lang.Code {
				// Untranslated: show the original with this language's
				// interface, and point search engines at the original.
				orig, _ := languageByCode(p.Lang)
				data["Canonical"] = orig.Prefix + "/posts/" + slug
				data["OnlyIn"] = lang.OnlyInText(p.Lang)
			}
			render(w, r, t, data)
		})

		mux.HandleFunc("GET "+lang.Prefix+"/feed.xml", func(w http.ResponseWriter, r *http.Request) {
			_, lib, ok := current(w)
			if !ok {
				return
			}
			var buf bytes.Buffer
			if err := writeFeed(&buf, base(r), lang, lib); err != nil {
				log.Println("Error writing feed:", err)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/atom+xml; charset=utf-8")
			w.Header().Set("Cache-Control", "public, max-age=3600")
			buf.WriteTo(w)
		})

		if lang.Prefix != "" {
			mux.Handle("GET "+lang.Prefix, http.RedirectHandler(lang.Prefix+"/", http.StatusMovedPermanently))
		}
		// One URL per page: a trailing slash redirects to the URL without.
		for _, path := range []string{"/posts", "/about", "/playground"} {
			mux.Handle("GET "+lang.Prefix+path+"/{$}", http.RedirectHandler(lang.Prefix+path, http.StatusMovedPermanently))
		}
	}

	for _, lang := range languages {
		mux.HandleFunc("GET "+lang.Prefix+"/about", func(w http.ResponseWriter, r *http.Request) {
			t, _, ok := current(w)
			if !ok {
				return
			}
			pages := about
			if *dev {
				p, err := loadPage(contentFS, "about")
				if err != nil {
					log.Println("Error loading about page:", err)
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				pages = p
			}
			page, ok := pages[lang.Code]
			if !ok {
				page = pages[defaultLanguage().Code]
			}
			data := pageData(r, lang, "/about", func(l Language) bool {
				_, ok := pages[l.Code]
				return ok
			})
			data["Page"] = page
			if page.Lang != lang.Code {
				orig, _ := languageByCode(page.Lang)
				data["Canonical"] = orig.Prefix + "/about"
				data["OnlyIn"] = lang.OnlyInPageText(page.Lang)
			}
			render(w, r, t, data)
		})
	}

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
		Handler:           gzhttp.GzipHandler(securityHeaders(mux)),
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

	// Page views are added up in Valkey every few seconds, if it is used;
	// a summary in the log is all there is to see of them.
	flushViews := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := views.Flush(ctx); err != nil {
			log.Println("Error saving page views in Valkey:", err)
		}
	}
	logViews := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if s := views.Summary(ctx, 10); s != "" {
			log.Println(s)
		}
	}
	go func() {
		flush, summary := time.Tick(10*time.Second), time.Tick(*viewsEvery)
		for {
			select {
			case <-flush:
				flushViews()
			case <-summary:
				flushViews()
				logViews()
			}
		}
	}()

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
	flushViews()
	logViews()
}
