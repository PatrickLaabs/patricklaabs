package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

const browserAgent = "Mozilla/5.0 (X11; Linux x86_64) Firefox/140.0"

func TestViewsCountAndSave(t *testing.T) {
	file := filepath.Join(t.TempDir(), "views.json")
	v, err := loadViews(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ method, path, agent string }{
		{"GET", "/", browserAgent},
		{"GET", "/de/posts/hello-world", browserAgent},
		{"GET", "/de/posts/hello-world", browserAgent},
		{"HEAD", "/", browserAgent},
		{"GET", "/", "Googlebot/2.1"},
		{"GET", "/", ""},
	} {
		r := httptest.NewRequest(c.method, c.path, nil)
		r.Header.Set("User-Agent", c.agent)
		v.Count(r)
	}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}

	loaded, err := loadViews(file)
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.data.Pages["/"]; got != 1 {
		t.Errorf("views of / = %d, want 1", got)
	}
	if got := loaded.data.Pages["/de/posts/hello-world"]; got != 2 {
		t.Errorf("views of /de/posts/hello-world = %d, want 2", got)
	}
}

func TestViewsHandlerPassword(t *testing.T) {
	v, _ := loadViews("")
	h := v.Handler("secret")

	w := httptest.NewRecorder()
	h(w, httptest.NewRequest("GET", "/stats", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("without password: status %d, want 401", w.Code)
	}

	r := httptest.NewRequest("GET", "/stats", nil)
	r.SetBasicAuth("patrick", "secret")
	w = httptest.NewRecorder()
	h(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Page views: 0") {
		t.Fatalf("with password: status %d, body %q", w.Code, w.Body.String())
	}
}
