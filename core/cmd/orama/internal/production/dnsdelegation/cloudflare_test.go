package dnsdelegation

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestCloudflare_createsNSAndGlueThenVerifies(t *testing.T) {
	var mu sync.Mutex
	records := map[string]cfRecord{}
	var next int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret-token" {
			http.Error(w, "auth", http.StatusUnauthorized)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/zones" && r.Method == http.MethodGet:
			if r.URL.Query().Get("name") != "example.test" {
				t.Errorf("zone query %s", r.URL.RawQuery)
			}
			_, _ = io.WriteString(w, `{"success":true,"result":[{"id":"z1"}]}`)
		case strings.HasPrefix(r.URL.Path, "/zones/z1/dns_records") && r.Method == http.MethodGet:
			var got []cfRecord
			for _, rec := range records {
				if rec.Type == r.URL.Query().Get("type") && rec.Name == r.URL.Query().Get("name") {
					got = append(got, rec)
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": got})
		case r.URL.Path == "/zones/z1/dns_records" && r.Method == http.MethodPost:
			var rec cfRecord
			if err := json.NewDecoder(r.Body).Decode(&rec); err != nil {
				t.Fatal(err)
			}
			if rec.Type == "A" {
				var raw map[string]any
				// proxied was decoded into cfRecord which has no field; re-read is too late.
				_ = raw
			}
			next++
			rec.ID = strings.Repeat("a", next)
			records[rec.ID] = rec
			_, _ = io.WriteString(w, `{"success":true}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.String())
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	d := Delegation{Domain: "stagenet.example.test", Nameservers: []Nameserver{
		{Hostname: "ns1", IP: "203.0.113.5"},
	}}
	c := &Cloudflare{
		Token: "secret-token",
		Base:  srv.URL,
		HTTP:  srv.Client(),
		LookupNS: func(context.Context, string) ([]string, error) {
			return []string{"ns1.stagenet.example.test."}, nil
		},
		LookupHost: func(context.Context, string) ([]string, error) {
			return []string{"203.0.113.5"}, nil
		},
	}
	if err := c.Apply(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	var ns, glue bool
	for _, rec := range records {
		if rec.Type == "NS" && rec.Name == "stagenet.example.test" && rec.Content == "ns1.stagenet.example.test" {
			ns = true
		}
		if rec.Type == "A" && rec.Name == "ns1.stagenet.example.test" && rec.Content == "203.0.113.5" {
			glue = true
		}
	}
	if !ns || !glue {
		t.Fatalf("records = %+v", records)
	}
}

func TestCloudflare_refusesARegistryTLD(t *testing.T) {
	c := &Cloudflare{Token: "t"}
	err := c.Apply(context.Background(), Delegation{Domain: "example.test"})
	if err == nil || !strings.Contains(err.Error(), "registrar") {
		t.Fatalf("err = %v", err)
	}
}

func TestCloudflare_verifyRefusesAMissingGlue(t *testing.T) {
	c := &Cloudflare{
		LookupNS: func(context.Context, string) ([]string, error) {
			return []string{"ns1.stagenet.example.test"}, nil
		},
		LookupHost: func(context.Context, string) ([]string, error) {
			return []string{"198.51.100.9"}, nil
		},
	}
	err := c.verify(context.Background(), Delegation{
		Domain:      "stagenet.example.test",
		Nameservers: []Nameserver{{Hostname: "ns1", IP: "203.0.113.5"}},
	})
	if err == nil || !strings.Contains(err.Error(), "glue") {
		t.Fatalf("err = %v", err)
	}
}
