package main

import (
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yuin/goldmark/text"
)

func TestSplitFrontMatter(t *testing.T) {
	tests := []struct {
		name, in string
		tags     []string
		body     string
		err      bool
	}{
		{name: "none", in: "# Title\n", body: "# Title\n"},
		{name: "yaml list", in: "---\ntags: [kubernetes, gateway-api]\n---\n\n# Title\n", tags: []string{"kubernetes", "gateway-api"}, body: "\n# Title\n"},
		{name: "plain list", in: "---\ntags: go, htmx\n---\n# T", tags: []string{"go", "htmx"}, body: "# T"},
		{name: "windows line ends", in: "---\r\ntags: [go]\r\n---\r\n# T", tags: []string{"go"}, body: "# T"},
		{name: "duplicates and quotes", in: "---\ntags: [go, \"go\", 'htmx']\n---\n", tags: []string{"go", "htmx"}, body: ""},
		{name: "upper case", in: "---\ntags: [Kubernetes]\n---\n", err: true},
		{name: "spaces", in: "---\ntags: [gateway api]\n---\n", err: true},
		{name: "unknown key", in: "---\ntitle: Hi\n---\n", err: true},
		{name: "unclosed", in: "---\ntags: [go]\n# Title\n", err: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fm, body, err := splitFrontMatter([]byte(tt.in))
			if tt.err {
				if err == nil {
					t.Fatalf("want an error, got tags %v", fm.Tags)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(fm.Tags, tt.tags) {
				t.Errorf("tags = %v, want %v", fm.Tags, tt.tags)
			}
			if string(body) != tt.body {
				t.Errorf("body = %q, want %q", body, tt.body)
			}
		})
	}
}

// Every post has tags, and its translations have the same ones, so the tag
// pages list the same posts in every language. A broken front matter would
// drop the post from the site, so it fails here first.
func TestPostTags(t *testing.T) {
	entries, err := fs.ReadDir(embedFS{}, "posts")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		data, err := fs.ReadFile(embedFS{}, "posts/"+e.Name())
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := splitFrontMatter(data); err != nil {
			t.Errorf("%s: %v", e.Name(), err)
		}
	}

	for slug, versions := range loadPosts(embedFS{}) {
		orig := versions[defaultLanguage().Code]
		if len(orig.Tags) == 0 {
			t.Errorf("post %q has no tags; add a line like tags: [kubernetes, probes] to its front matter, see WRITING.md", slug)
		}
		for lang, p := range versions {
			if !reflect.DeepEqual(p.Tags, orig.Tags) {
				t.Errorf("post %q: %s tags %v differ from %s tags %v; tags are the same in every language", slug, lang, p.Tags, orig.Lang, orig.Tags)
			}
		}
	}
}

func TestSearch(t *testing.T) {
	day := func(d int) time.Time { return time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC) }
	posts := []Post{
		{Slug: "new", Title: "Gateway API, live", Tags: []string{"networking"}, Date: day(22), text: "routes and probes for a shop"},
		{Slug: "mid", Title: "Readiness probes", Tags: []string{"probes"}, Date: day(20), text: "a readiness probe keeps traffic away"},
		{Slug: "old", Title: "Hello", Tags: []string{"blog"}, Date: day(16), text: "welcome"},
	}
	slugs := func(ps []Post) []string {
		var s []string
		for _, p := range ps {
			s = append(s, p.Slug)
		}
		return s
	}
	tests := []struct {
		query string
		want  []string
	}{
		{"", []string{"new", "mid", "old"}},
		{"   ", []string{"new", "mid", "old"}},
		// A hit in the title ranks above one in the text.
		{"PROBES", []string{"mid", "new"}},
		{"probe traffic", []string{"mid"}},
		{"networking", []string{"new"}},
		{"probes nothing", nil},
	}
	for _, tt := range tests {
		if got := slugs(search(posts, tt.query)); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("search(%q) = %v, want %v", tt.query, got, tt.want)
		}
	}
}

func TestTags(t *testing.T) {
	lib := loadPosts(embedFS{})
	tags := lib.tags("en")
	if len(tags) == 0 {
		t.Fatal("no tags")
	}
	for i := 1; i < len(tags); i++ {
		if tags[i].Count > tags[i-1].Count {
			t.Errorf("tags not sorted by count: %v", tags)
		}
	}
	for _, tc := range tags {
		if n := len(withTag(lib.list("de"), tc.Name)); n != tc.Count {
			t.Errorf("tag %s: %d German posts, %d English", tc.Name, n, tc.Count)
		}
		if !lib.hasTag(tc.Name, "de") {
			t.Errorf("tag %s has no German post", tc.Name)
		}
	}
}

func TestSearchTextIncludesCode(t *testing.T) {
	data := []byte("# Title\n\nSome *text*.\n\n```yaml\nreadinessProbe:\n  httpGet: {}\n```\n\nEnd.")
	doc := md.Parser().Parse(text.NewReader(data))
	got := searchText(doc, data)
	for _, want := range []string{"title some text.", "readinessprobe:", "end."} {
		if !strings.Contains(got, want) {
			t.Errorf("searchText = %q, want it to contain %q", got, want)
		}
	}
}
