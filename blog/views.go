package main

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
)

// Views counts page views in memory, from the start of the process, without
// storing anything about the visitor: no cookies, no IP addresses, no user
// agents. Only the path of each rendered page is kept, and the counts are
// gone when the pod restarts.
type Views struct {
	mu     sync.Mutex
	pages  map[string]int64 // views per path, like /de/posts/hello-world
	total  int64
	logged int64 // total at the last summary
}

func newViews() *Views {
	return &Views{pages: map[string]int64{}}
}

// botAgents match user agents of crawlers, link previews and uptime checks,
// which would otherwise drown out real readers.
var botAgents = []string{"bot", "crawl", "spider", "slurp", "preview", "monitor", "kube-probe", "curl", "wget", "python", "go-http-client"}

func isBot(userAgent string) bool {
	ua := strings.ToLower(userAgent)
	if ua == "" {
		return true
	}
	for _, b := range botAgents {
		if strings.Contains(ua, b) {
			return true
		}
	}
	return false
}

// Count records one view of the page r asked for. Only GET requests from
// browsers count; the path must be one the server actually rendered, so the
// number of distinct paths stays bounded by the number of pages.
func (v *Views) Count(r *http.Request) {
	if r.Method != http.MethodGet || isBot(r.UserAgent()) {
		return
	}
	v.mu.Lock()
	v.pages[r.URL.Path]++
	v.total++
	v.mu.Unlock()
}

// Summary returns one log line with the views since the process started,
// the views since the previous summary and the most viewed pages. It
// returns "" if nothing was viewed since the previous summary.
func (v *Views) Summary(top int) string {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.total == v.logged {
		return ""
	}
	type row struct {
		path string
		n    int64
	}
	rows := make([]row, 0, len(v.pages))
	for p, n := range v.pages {
		rows = append(rows, row{p, n})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].n != rows[j].n {
			return rows[i].n > rows[j].n
		}
		return rows[i].path < rows[j].path
	})
	if len(rows) > top {
		rows = rows[:top]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Page views since start: %d (+%d since last summary); top:", v.total, v.total-v.logged)
	for _, r := range rows {
		fmt.Fprintf(&b, " %s=%d", r.path, r.n)
	}
	v.logged = v.total
	return b.String()
}
