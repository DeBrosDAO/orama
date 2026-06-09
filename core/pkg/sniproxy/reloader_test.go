package sniproxy

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
)

// feat-41: the SNI router hot-reloads its route table from disk so a namespace's
// cdn/turn routes can be added/removed without restarting the router. These pin
// the initial apply, the hot-reload-on-change path, and the resilience contract
// (a bad source keeps the currently-installed routes serving).

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

func TestFileRouteReloader_appliesInitialRoutes(t *testing.T) {
	path := writeFile(t, t.TempDir(), "routes.yaml", "v1")
	source := func() ([]Route, Backend, error) {
		return []Route{
			{Match: "cdn.ns-a.example.com", Backend: Backend{Addr: "127.0.0.1:5349"}},
		}, Backend{Addr: "127.0.0.1:8443"}, nil
	}
	router := NewRouter(Backend{Addr: "unset"})
	r := NewFileRouteReloader(path, source, router, zap.NewNop())

	if err := r.Apply(); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := len(router.Routes()); got != 1 {
		t.Fatalf("want 1 route after initial apply, got %d", got)
	}
	if b := router.Pick("cdn.ns-a.example.com"); b.Addr != "127.0.0.1:5349" {
		t.Errorf("route not installed; Pick gave %q", b.Addr)
	}
	if router.Fallback().Addr != "127.0.0.1:8443" {
		t.Errorf("fallback not installed; got %q", router.Fallback().Addr)
	}
}

func TestFileRouteReloader_hotReloadsOnFileChange(t *testing.T) {
	path := writeFile(t, t.TempDir(), "routes.yaml", "v1")

	var mu sync.Mutex
	version := 1
	source := func() ([]Route, Backend, error) {
		mu.Lock()
		defer mu.Unlock()
		if version == 1 {
			return []Route{{Match: "a.example.com", Backend: Backend{Addr: "127.0.0.1:1"}}},
				Backend{Addr: "fb:1"}, nil
		}
		return []Route{
			{Match: "a.example.com", Backend: Backend{Addr: "127.0.0.1:1"}},
			{Match: "b.example.com", Backend: Backend{Addr: "127.0.0.1:2"}},
		}, Backend{Addr: "fb:2"}, nil
	}
	router := NewRouter(Backend{Addr: "unset"})
	r := NewFileRouteReloader(path, source, router, zap.NewNop())
	if err := r.Apply(); err != nil {
		t.Fatalf("initial Apply: %v", err)
	}
	if len(router.Routes()) != 1 {
		t.Fatalf("want 1 route initially, got %d", len(router.Routes()))
	}

	// "Renew": flip the source to v2 and advance the file mtime so the watcher
	// detects the change regardless of filesystem timestamp granularity.
	mu.Lock()
	version = 2
	mu.Unlock()
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	stop := make(chan struct{})
	defer close(stop)
	go r.Watch(5*time.Millisecond, stop)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(router.Routes()) == 2 && router.Fallback().Addr == "fb:2" {
			return // hot-reloaded
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("routes were not hot-reloaded (have %d routes, fallback %q)",
		len(router.Routes()), router.Fallback().Addr)
}

func TestFileRouteReloader_keepsRoutesOnSourceError(t *testing.T) {
	path := writeFile(t, t.TempDir(), "routes.yaml", "v1")

	var mu sync.Mutex
	fail := false
	source := func() ([]Route, Backend, error) {
		mu.Lock()
		defer mu.Unlock()
		if fail {
			return nil, Backend{}, errors.New("invalid config")
		}
		return []Route{{Match: "a.example.com", Backend: Backend{Addr: "127.0.0.1:1"}}},
			Backend{Addr: "fb:1"}, nil
	}
	router := NewRouter(Backend{Addr: "unset"})
	r := NewFileRouteReloader(path, source, router, zap.NewNop())
	if err := r.Apply(); err != nil {
		t.Fatalf("initial Apply: %v", err)
	}

	// Make the source fail, then trigger a reload via an mtime bump.
	mu.Lock()
	fail = true
	mu.Unlock()
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	stop := make(chan struct{})
	go r.Watch(5*time.Millisecond, stop)
	time.Sleep(200 * time.Millisecond) // let it tick + hit the failing source
	close(stop)

	if got := len(router.Routes()); got != 1 {
		t.Errorf("a failed reload must keep the previous routes; got %d routes", got)
	}
	if router.Fallback().Addr != "fb:1" {
		t.Errorf("a failed reload must keep the previous fallback; got %q", router.Fallback().Addr)
	}
}
