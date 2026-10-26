package main

import (
	"encoding/xml"
	"io"
	"strings"
	"time"
)

// defaultBaseURL is where the blog is published. Absolute URLs in canonical
// and hreflang links, the sitemap, the feeds and link previews are built
// from it rather than from the request, so they never point at another host
// or at plain http behind a proxy.
const defaultBaseURL = "https://patrick.laabs.dev"

// author is who every post is by, for structured data and the feeds.
const author = "Patrick Laabs"

// authorProfiles are other pages about the author, for structured data.
var authorProfiles = []string{"https://github.com/PatrickLaabs"}

// staticPaths are the pages besides posts, without a language prefix. Every
// one of them exists in every language.
var staticPaths = []string{"/", "/posts", "/about", "/playground"}

// description shortens a first paragraph to the length search engines show
// in results, about 160 characters. It keeps whole sentences when that still
// leaves a useful text, and cuts at a word otherwise.
func description(s string) string {
	const max, min = 160, 70
	if len(s) <= max {
		return s
	}
	cut := ""
	for i := 0; i < len(s) && i < max; i++ {
		if (s[i] == '.' || s[i] == '!' || s[i] == '?') && (i+1 == len(s) || s[i+1] == ' ') {
			cut = s[:i+1]
		}
	}
	if len(cut) >= min {
		return cut
	}
	return truncate(s, max-3)
}

// robotsTxt allows everything except the HTMX demo endpoint and points
// crawlers at the sitemap.
func robotsTxt(base string) string {
	return "User-agent: *\nDisallow: /clicked\n\nSitemap: " + base + "/sitemap.xml\n"
}

type sitemapURL struct {
	Loc        string        `xml:"loc"`
	LastMod    string        `xml:"lastmod,omitempty"`
	Alternates []sitemapLink `xml:"xhtml:link"`
}

type sitemapLink struct {
	Rel      string `xml:"rel,attr"`
	Hreflang string `xml:"hreflang,attr"`
	Href     string `xml:"href,attr"`
}

// writeSitemap lists every page in every language it exists in, each with
// links to its translations, like the hreflang links in the page head.
func writeSitemap(w io.Writer, base string, lib Library) error {
	var urls []sitemapURL
	add := func(path string, exists func(Language) bool, lastMod time.Time) {
		var alts []sitemapLink
		for _, l := range languages {
			if exists(l) {
				alts = append(alts, sitemapLink{"alternate", l.Code, base + l.Prefix + path})
			}
		}
		if len(alts) > 1 {
			alts = append(alts, sitemapLink{"alternate", "x-default", alts[0].Href})
		} else {
			alts = nil
		}
		for _, l := range languages {
			if !exists(l) {
				continue
			}
			u := sitemapURL{Loc: base + l.Prefix + path, Alternates: alts}
			if !lastMod.IsZero() {
				u.LastMod = lastMod.Format(time.DateOnly)
			}
			urls = append(urls, u)
		}
	}

	posts := lib.list(defaultLanguage().Code)
	var newest time.Time
	if len(posts) > 0 {
		newest = posts[0].Date
	}
	for _, path := range staticPaths {
		lastMod := time.Time{}
		if path == "/" || path == "/posts" {
			lastMod = newest
		}
		add(path, func(Language) bool { return true }, lastMod)
	}
	for _, p := range posts {
		add("/posts/"+p.Slug, func(l Language) bool {
			_, ok := lib[p.Slug][l.Code]
			return ok
		}, p.Date)
	}

	io.WriteString(w, xml.Header)
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	return enc.Encode(struct {
		XMLName xml.Name     `xml:"urlset"`
		NS      string       `xml:"xmlns,attr"`
		XHTML   string       `xml:"xmlns:xhtml,attr"`
		URLs    []sitemapURL `xml:"url"`
	}{
		NS:    "http://www.sitemaps.org/schemas/sitemap/0.9",
		XHTML: "http://www.w3.org/1999/xhtml",
		URLs:  urls,
	})
}

type atomLink struct {
	Rel  string `xml:"rel,attr,omitempty"`
	Type string `xml:"type,attr,omitempty"`
	Href string `xml:"href,attr"`
}

type atomText struct {
	Type string `xml:"type,attr,omitempty"`
	Body string `xml:",chardata"`
}

type atomEntry struct {
	Title     string   `xml:"title"`
	Link      atomLink `xml:"link"`
	ID        string   `xml:"id"`
	Published string   `xml:"published"`
	Updated   string   `xml:"updated"`
	Summary   string   `xml:"summary"`
	Content   atomText `xml:"content"`
}

// writeFeed writes the Atom feed of every post in lang, newest first, with
// the full post as HTML so it reads well in feed readers.
func writeFeed(w io.Writer, base string, lang Language, lib Library) error {
	posts := lib.list(lang.Code)
	self := base + lang.Prefix + "/feed.xml"
	updated := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if len(posts) > 0 {
		updated = posts[0].Date
	}
	var entries []atomEntry
	for _, p := range posts {
		url := base + lang.Prefix + "/posts/" + p.Slug
		entries = append(entries, atomEntry{
			Title:     p.Title,
			Link:      atomLink{Rel: "alternate", Type: "text/html", Href: url},
			ID:        url,
			Published: p.Date.Format(time.RFC3339),
			Updated:   p.Date.Format(time.RFC3339),
			Summary:   p.Summary,
			Content:   atomText{Type: "html", Body: string(p.Content)},
		})
	}

	io.WriteString(w, xml.Header)
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	return enc.Encode(struct {
		XMLName  xml.Name    `xml:"feed"`
		NS       string      `xml:"xmlns,attr"`
		Lang     string      `xml:"xml:lang,attr"`
		Base     string      `xml:"xml:base,attr"`
		Title    string      `xml:"title"`
		Subtitle string      `xml:"subtitle"`
		Links    []atomLink  `xml:"link"`
		ID       string      `xml:"id"`
		Updated  string      `xml:"updated"`
		Author   string      `xml:"author>name"`
		Entries  []atomEntry `xml:"entry"`
	}{
		NS:       "http://www.w3.org/2005/Atom",
		Lang:     lang.Code,
		Base:     base + "/",
		Title:    lang.Text.SiteTitle,
		Subtitle: lang.Text.SiteDescription,
		Links: []atomLink{
			{Rel: "self", Type: "application/atom+xml", Href: self},
			{Rel: "alternate", Type: "text/html", Href: base + lang.Prefix + "/"},
		},
		ID:      self,
		Updated: updated.Format(time.RFC3339),
		Author:  author,
		Entries: entries,
	})
}

// person describes the author for structured data.
func person(base, lang string) map[string]any {
	prefix := ""
	if l, ok := languageByCode(lang); ok {
		prefix = l.Prefix
	}
	return map[string]any{
		"@type":  "Person",
		"name":   author,
		"url":    base + prefix + "/about",
		"sameAs": authorProfiles,
	}
}

// postJSONLD is the schema.org BlogPosting of a post, which search engines
// use for rich results like the publish date and author.
func postJSONLD(base, url string, p Post) map[string]any {
	date := p.Date.Format(time.DateOnly)
	return map[string]any{
		"@context":         "https://schema.org",
		"@type":            "BlogPosting",
		"headline":         p.Title,
		"description":      p.Description,
		"datePublished":    date,
		"dateModified":     date,
		"inLanguage":       p.Lang,
		"url":              url,
		"mainEntityOfPage": url,
		"image":            base + "/static/og.png",
		"author":           person(base, p.Lang),
		"publisher":        person(base, p.Lang),
	}
}

// siteJSONLD is the schema.org WebSite of the front page in lang.
func siteJSONLD(base string, lang Language) map[string]any {
	return map[string]any{
		"@context":    "https://schema.org",
		"@type":       "WebSite",
		"name":        lang.Text.SiteTitle,
		"description": lang.Text.SiteDescription,
		"url":         base + lang.Prefix + "/",
		"inLanguage":  lang.Code,
		"author":      person(base, lang.Code),
	}
}

// hasPrefixDir reports whether path is prefix itself or below it, so /de
// matches /de and /de/posts but not /demo.
func hasPrefixDir(path, prefix string) bool {
	return prefix != "" && (path == prefix || strings.HasPrefix(path, prefix+"/"))
}

// languageOfPath returns the language whose URL prefix path starts with.
func languageOfPath(path string) Language {
	for _, l := range languages {
		if hasPrefixDir(path, l.Prefix) {
			return l
		}
	}
	return defaultLanguage()
}
