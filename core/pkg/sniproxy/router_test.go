package sniproxy

import (
	"sync"
	"testing"
)

func TestRouter_pick_exact_match(t *testing.T) {
	fb := Backend{Name: "fallback", Addr: "127.0.0.1:9000"}
	r := NewRouter(fb)
	r.Replace([]Route{
		{Match: "turn.example.com", Backend: Backend{Name: "turn", Addr: "127.0.0.1:5349"}},
	}, fb)

	got := r.Pick("turn.example.com")
	if got.Addr != "127.0.0.1:5349" {
		t.Errorf("expected turn backend, got %+v", got)
	}
}

func TestRouter_pick_unmatched_returns_fallback(t *testing.T) {
	fb := Backend{Name: "caddy", Addr: "127.0.0.1:8443"}
	r := NewRouter(fb)
	r.Replace([]Route{
		{Match: "turn.example.com", Backend: Backend{Addr: "127.0.0.1:5349"}},
	}, fb)

	if got := r.Pick("api.example.com"); got != fb {
		t.Errorf("expected fallback, got %+v", got)
	}
	if got := r.Pick(""); got != fb {
		t.Errorf("expected fallback for empty SNI, got %+v", got)
	}
}

func TestRouter_pick_case_insensitive(t *testing.T) {
	fb := Backend{Addr: "127.0.0.1:8443"}
	r := NewRouter(fb)
	r.Replace([]Route{
		{Match: "Turn.Example.Com", Backend: Backend{Addr: "127.0.0.1:5349"}},
	}, fb)

	if got := r.Pick("turn.example.com"); got.Addr != "127.0.0.1:5349" {
		t.Errorf("expected case-insensitive match, got %+v", got)
	}
}

func TestRouter_pick_wildcard_subdomain(t *testing.T) {
	fb := Backend{Addr: "127.0.0.1:8443"}
	r := NewRouter(fb)
	r.Replace([]Route{
		{Match: "*.example.com", Backend: Backend{Name: "wild", Addr: "127.0.0.1:5349"}},
	}, fb)

	cases := map[string]bool{
		"a.example.com":   true,
		"foo.example.com": true,
		"a.b.example.com": false, // multi-label not allowed
		"example.com":     false, // bare domain doesn't match *.example.com
		"other.com":       false,
	}
	for sni, want := range cases {
		got := r.Pick(sni) == Backend{Name: "wild", Addr: "127.0.0.1:5349"}
		if got != want {
			t.Errorf("Pick(%q): want match=%v, got match=%v", sni, want, got)
		}
	}
}

func TestRouter_replace_atomic(t *testing.T) {
	// Many concurrent reads against many concurrent Replace calls — should
	// never observe partial state. Run with -race.
	fb := Backend{Addr: "fb"}
	r := NewRouter(fb)
	r.Replace([]Route{{Match: "a.com", Backend: Backend{Addr: "1"}}}, fb)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Readers
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = r.Pick("a.com")
				}
			}
		}()
	}

	// Writers
	for i := 0; i < 200; i++ {
		r.Replace([]Route{{Match: "a.com", Backend: Backend{Addr: "x"}}}, fb)
	}
	close(stop)
	wg.Wait()
}

func TestRouter_routes_returns_copy(t *testing.T) {
	r := NewRouter(Backend{})
	original := []Route{{Match: "a", Backend: Backend{Addr: "1"}}}
	r.Replace(original, Backend{})
	got := r.Routes()
	got[0].Match = "mutated"
	if r.Routes()[0].Match != "a" {
		t.Error("Routes() should return a defensive copy")
	}
}
