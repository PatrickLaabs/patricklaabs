package main

import (
	"net/http/httptest"
	"testing"
)

const browserAgent = "Mozilla/5.0 (X11; Linux x86_64) Firefox/140.0"

func TestViewsSummary(t *testing.T) {
	v := newViews()
	if got := v.Summary(10); got != "" {
		t.Errorf("summary without views = %q, want empty", got)
	}
	for _, c := range []struct{ method, path, agent string }{
		{"GET", "/", browserAgent},
		{"GET", "/de/posts/hello-world", browserAgent},
		{"GET", "/de/posts/hello-world", browserAgent},
		{"HEAD", "/", browserAgent},
		{"GET", "/", "Googlebot/2.1"},
		{"GET", "/", "kube-probe/1.34"},
		{"GET", "/", ""},
	} {
		r := httptest.NewRequest(c.method, c.path, nil)
		r.Header.Set("User-Agent", c.agent)
		v.Count(r)
	}

	want := "Page views since start: 3 (+3 since last summary); top: /de/posts/hello-world=2 /=1"
	if got := v.Summary(10); got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
	if got := v.Summary(10); got != "" {
		t.Errorf("summary without new views = %q, want empty", got)
	}

	r := httptest.NewRequest("GET", "/about", nil)
	r.Header.Set("User-Agent", browserAgent)
	v.Count(r)
	want = "Page views since start: 4 (+1 since last summary); top: /de/posts/hello-world=2"
	if got := v.Summary(1); got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
}
