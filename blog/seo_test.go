package main

import (
	"bytes"
	"encoding/xml"
	"strings"
	"testing"
)

func TestDescription(t *testing.T) {
	short := "Short enough."
	if got := description(short); got != short {
		t.Errorf("description(%q) = %q, want it unchanged", short, got)
	}

	// Whole sentences are kept when they fit.
	first := "Kubernetes can tell whether your container is running, but not whether your app actually works. Probes fill that gap."
	long := first + " In this post you see, step by step and live in your browser, which probe to use when."
	if got := description(long); got != first {
		t.Errorf("description kept %q, want the first two sentences", got)
	}

	// A first sentence that is too long is cut at a word.
	words := strings.Repeat("word ", 50)
	got := description(words)
	if len(got) > 160 || !strings.HasSuffix(got, "…") {
		t.Errorf("description(%q) = %q, want at most 160 bytes ending in …", words, got)
	}
}

// Every post has a description of a length search engines show in full.
func TestPostDescriptions(t *testing.T) {
	for slug, versions := range loadPosts(embedFS{}) {
		for lang, p := range versions {
			if n := len([]rune(p.Description)); n < 50 || n > 160 {
				t.Errorf("post %s (%s): description has %d characters, want 50 to 160: %q", slug, lang, n, p.Description)
			}
		}
	}
}

func TestSitemap(t *testing.T) {
	lib := loadPosts(embedFS{})
	var buf bytes.Buffer
	if err := writeSitemap(&buf, "https://example.com", lib); err != nil {
		t.Fatal(err)
	}
	var sitemap struct {
		URLs []struct {
			Loc string `xml:"loc"`
		} `xml:"url"`
	}
	if err := xml.Unmarshal(buf.Bytes(), &sitemap); err != nil {
		t.Fatalf("sitemap is not valid XML: %v", err)
	}
	locs := map[string]bool{}
	for _, u := range sitemap.URLs {
		locs[u.Loc] = true
	}
	want := []string{"https://example.com/", "https://example.com/de/", "https://example.com/de/about"}
	for slug := range lib {
		want = append(want, "https://example.com/posts/"+slug, "https://example.com/de/posts/"+slug)
	}
	for _, w := range want {
		if !locs[w] {
			t.Errorf("sitemap is missing %s", w)
		}
	}
}

func TestFeed(t *testing.T) {
	lib := loadPosts(embedFS{})
	for _, l := range languages {
		var buf bytes.Buffer
		if err := writeFeed(&buf, "https://example.com", l, lib); err != nil {
			t.Fatal(err)
		}
		var feed struct {
			Entries []struct {
				ID string `xml:"id"`
			} `xml:"entry"`
		}
		if err := xml.Unmarshal(buf.Bytes(), &feed); err != nil {
			t.Fatalf("%s feed is not valid XML: %v", l.Code, err)
		}
		if len(feed.Entries) != len(lib) {
			t.Errorf("%s feed has %d entries, want %d", l.Code, len(feed.Entries), len(lib))
		}
	}
}

func TestRobotsTxtPointsAtSitemap(t *testing.T) {
	if got := robotsTxt("https://example.com"); !strings.Contains(got, "Sitemap: https://example.com/sitemap.xml") {
		t.Errorf("robots.txt does not name the sitemap:\n%s", got)
	}
}

func TestLanguageOfPath(t *testing.T) {
	for path, want := range map[string]string{"/de": "de", "/de/nope": "de", "/demo": "en", "/nope": "en"} {
		if got := languageOfPath(path).Code; got != want {
			t.Errorf("languageOfPath(%q) = %s, want %s", path, got, want)
		}
	}
}
