package main

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/valkey-io/valkey-go"
)

// viewsKey is the Valkey hash that holds the views per path.
const viewsKey = "blog:views"

// Views counts page views without storing anything about the visitor: no
// cookies, no IP addresses, no user agents. Only the path of each rendered
// page is kept. The counts live in memory, from the start of the process,
// and are also added up in Valkey if an address is given, so they survive
// restarts and cover every replica.
type Views struct {
	mu      sync.Mutex
	pages   map[string]int64 // views per path on this pod, like /de/posts/hello-world
	total   int64
	logged  int64            // total at the last summary
	pending map[string]int64 // views not yet added in Valkey

	addr   string // Valkey address; empty keeps the counts in memory only
	client valkey.Client
}

func newViews(addr string) *Views {
	return &Views{pages: map[string]int64{}, pending: map[string]int64{}, addr: addr}
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
	if v.addr != "" {
		v.pending[r.URL.Path]++
	}
	v.mu.Unlock()
}

// connect returns the Valkey client, connecting first if needed, so the
// blog starts and keeps counting even while Valkey is down.
func (v *Views) connect() (valkey.Client, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.client != nil {
		return v.client, nil
	}
	c, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{v.addr}, DisableCache: true})
	if err != nil {
		return nil, err
	}
	v.client = c
	return c, nil
}

// Flush adds the views counted since the last flush in Valkey. On failure
// they are kept and added on the next try.
func (v *Views) Flush(ctx context.Context) error {
	if v.addr == "" {
		return nil
	}
	v.mu.Lock()
	pending := v.pending
	v.pending = map[string]int64{}
	v.mu.Unlock()
	if len(pending) == 0 {
		return nil
	}
	c, err := v.connect()
	if err == nil {
		paths := make([]string, 0, len(pending))
		cmds := make(valkey.Commands, 0, len(pending))
		for path, n := range pending {
			paths = append(paths, path)
			cmds = append(cmds, c.B().Hincrby().Key(viewsKey).Field(path).Increment(n).Build())
		}
		for i, resp := range c.DoMulti(ctx, cmds...) {
			if resp.Error() != nil {
				err = resp.Error()
				continue
			}
			// Done paths are removed, so a retry never counts them twice.
			delete(pending, paths[i])
		}
	}
	if err != nil {
		v.mu.Lock()
		for path, n := range pending {
			v.pending[path] += n
		}
		v.mu.Unlock()
	}
	return err
}

// Summary returns one log line with the views since the previous summary
// and the most viewed pages: of all time in Valkey if it is used and
// reachable, otherwise of this pod since it started. It returns "" if
// nothing was viewed on this pod since the previous summary.
func (v *Views) Summary(ctx context.Context, top int) string {
	v.mu.Lock()
	since := v.total - v.logged
	v.logged = v.total
	counts := make(map[string]int64, len(v.pages))
	for p, n := range v.pages {
		counts[p] = n
	}
	total, scope := v.total, "since start"
	v.mu.Unlock()
	if since == 0 {
		return ""
	}

	if v.addr != "" {
		if all, err := v.stored(ctx); err == nil {
			counts, scope = all, "in total"
			total = 0
			for _, n := range counts {
				total += n
			}
		} else {
			scope = fmt.Sprintf("since start (Valkey: %v)", err)
		}
	}

	type row struct {
		path string
		n    int64
	}
	rows := make([]row, 0, len(counts))
	for p, n := range counts {
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
	fmt.Fprintf(&b, "Page views %s: %d (+%d on this pod since last summary); top:", scope, total, since)
	for _, r := range rows {
		fmt.Fprintf(&b, " %s=%d", r.path, r.n)
	}
	return b.String()
}

// stored returns the views per path that are saved in Valkey.
func (v *Views) stored(ctx context.Context) (map[string]int64, error) {
	c, err := v.connect()
	if err != nil {
		return nil, err
	}
	m, err := c.Do(ctx, c.B().Hgetall().Key(viewsKey).Build()).AsStrMap()
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int64, len(m))
	for p, s := range m {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%s %s: %w", viewsKey, p, err)
		}
		counts[p] = n
	}
	return counts, nil
}

// Close closes the connection to Valkey, if there is one.
func (v *Views) Close() {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.client != nil {
		v.client.Close()
	}
}
