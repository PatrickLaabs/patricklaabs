package main

import (
	"context"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const browserAgent = "Mozilla/5.0 (X11; Linux x86_64) Firefox/140.0"

func view(v *Views, method, path, agent string) {
	r := httptest.NewRequest(method, path, nil)
	r.Header.Set("User-Agent", agent)
	v.Count(r)
}

func TestViewsSummary(t *testing.T) {
	ctx := context.Background()
	v := newViews("")
	if got := v.Summary(ctx, 10); got != "" {
		t.Errorf("summary without views = %q, want empty", got)
	}
	view(v, "GET", "/", browserAgent)
	view(v, "GET", "/de/posts/hello-world", browserAgent)
	view(v, "GET", "/de/posts/hello-world", browserAgent)
	view(v, "HEAD", "/", browserAgent)
	view(v, "GET", "/", "Googlebot/2.1")
	view(v, "GET", "/", "kube-probe/1.34")
	view(v, "GET", "/", "")

	want := "Page views since start: 3 (+3 on this pod since last summary); top: /de/posts/hello-world=2 /=1"
	if got := v.Summary(ctx, 10); got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
	if got := v.Summary(ctx, 10); got != "" {
		t.Errorf("summary without new views = %q, want empty", got)
	}

	view(v, "GET", "/about", browserAgent)
	want = "Page views since start: 4 (+1 on this pod since last summary); top: /de/posts/hello-world=2"
	if got := v.Summary(ctx, 1); got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
}

// TestViewsValkey runs against a throwaway valkey-server or redis-server,
// if one is installed.
func TestViewsValkey(t *testing.T) {
	var server string
	for _, name := range []string{"valkey-server", "redis-server"} {
		if p, err := exec.LookPath(name); err == nil {
			server = p
			break
		}
	}
	if server == "" {
		t.Skip("no valkey-server or redis-server installed")
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	port := addr[strings.LastIndexByte(addr, ':')+1:]
	l.Close()
	cmd := exec.Command(server, "--port", port, "--save", "", "--appendonly", "no", "--dir", t.TempDir())
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	for i := 0; ; i++ {
		if c, err := net.Dial("tcp", addr); err == nil {
			c.Close()
			break
		}
		if i == 50 {
			t.Fatal("server did not start")
		}
		time.Sleep(100 * time.Millisecond)
	}

	ctx := context.Background()
	// Two replicas, one restarted in between: the totals add up.
	a, b := newViews(addr), newViews(addr)
	defer a.Close()
	defer b.Close()
	view(a, "GET", "/", browserAgent)
	view(a, "GET", "/posts/x", browserAgent)
	view(b, "GET", "/", browserAgent)
	for _, v := range []*Views{a, b} {
		if err := v.Flush(ctx); err != nil {
			t.Fatal(err)
		}
	}
	c := newViews(addr)
	defer c.Close()
	view(c, "GET", "/", browserAgent)
	if err := c.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	want := "Page views in total: 4 (+1 on this pod since last summary); top: /=3 /posts/x=1"
	if got := c.Summary(ctx, 10); got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
}

func TestViewsValkeyDown(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	v := newViews("127.0.0.1:1")
	defer v.Close()
	view(v, "GET", "/", browserAgent)
	if err := v.Flush(ctx); err == nil {
		t.Fatal("flush to a closed port succeeded")
	}
	if v.pending["/"] != 1 {
		t.Errorf("pending views of / = %d, want 1 after a failed flush", v.pending["/"])
	}
	if got := v.Summary(ctx, 10); !strings.HasPrefix(got, "Page views since start (Valkey: ") {
		t.Errorf("summary = %q, want the counts since start with the Valkey error", got)
	}
}
