package main

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Views counts page views without storing anything about the visitor: no
// cookies, no IP addresses, no user agents. Only the path of each rendered
// page and the number of views per day are kept.
type Views struct {
	mu    sync.Mutex
	file  string // where the counts are saved; empty keeps them in memory
	dirty bool
	data  viewData
}

type viewData struct {
	Pages map[string]int64 `json:"pages"` // views per path, like /de/posts/hello-world
	Days  map[string]int64 `json:"days"`  // views per UTC day, like 2026-10-21
}

// loadViews reads the counts saved in file. A missing file starts from zero.
func loadViews(file string) (*Views, error) {
	v := &Views{file: file, data: viewData{Pages: map[string]int64{}, Days: map[string]int64{}}}
	if file == "" {
		return v, nil
	}
	b, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return v, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &v.data); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	if v.data.Pages == nil {
		v.data.Pages = map[string]int64{}
	}
	if v.data.Days == nil {
		v.data.Days = map[string]int64{}
	}
	return v, nil
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
	day := time.Now().UTC().Format(time.DateOnly)
	v.mu.Lock()
	v.data.Pages[r.URL.Path]++
	v.data.Days[day]++
	v.dirty = true
	v.mu.Unlock()
}

// Save writes the counts to the file if they changed since the last save.
// It writes a temporary file first, so a crash never leaves a broken file.
func (v *Views) Save() error {
	if v.file == "" {
		return nil
	}
	v.mu.Lock()
	if !v.dirty {
		v.mu.Unlock()
		return nil
	}
	b, err := json.MarshalIndent(v.data, "", "  ")
	v.dirty = false
	v.mu.Unlock()
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(v.file), ".views-*.json")
	if err == nil {
		_, err = tmp.Write(b)
		if cerr := tmp.Close(); err == nil {
			err = cerr
		}
		if err == nil {
			err = os.Rename(tmp.Name(), v.file)
		}
		if err != nil {
			os.Remove(tmp.Name())
		}
	}
	if err != nil {
		v.mu.Lock()
		v.dirty = true
		v.mu.Unlock()
	}
	return err
}

// Handler serves the counts as plain text: the total, the last 30 days and
// every page, most viewed first. If password is set, it is required via
// HTTP basic auth (any user name).
func (v *Views) Handler(password string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if password != "" {
			_, pass, _ := r.BasicAuth()
			if subtle.ConstantTimeCompare([]byte(pass), []byte(password)) != 1 {
				w.Header().Set("WWW-Authenticate", `Basic realm="stats"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
		}
		type row struct {
			key string
			n   int64
		}
		v.mu.Lock()
		var total int64
		pages := make([]row, 0, len(v.data.Pages))
		for k, n := range v.data.Pages {
			pages = append(pages, row{k, n})
			total += n
		}
		days := make([]row, 0, 30)
		today := time.Now().UTC()
		for i := range 30 {
			d := today.AddDate(0, 0, -i).Format(time.DateOnly)
			days = append(days, row{d, v.data.Days[d]})
		}
		v.mu.Unlock()
		sort.Slice(pages, func(i, j int) bool {
			if pages[i].n != pages[j].n {
				return pages[i].n > pages[j].n
			}
			return pages[i].key < pages[j].key
		})

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Robots-Tag", "noindex")
		fmt.Fprintf(w, "Page views: %d\n\nLast 30 days (UTC)\n", total)
		for _, d := range days {
			fmt.Fprintf(w, "  %s  %8d\n", d.key, d.n)
		}
		fmt.Fprintln(w, "\nPages")
		for _, p := range pages {
			fmt.Fprintf(w, "  %8d  %s\n", p.n, p.key)
		}
	}
}
