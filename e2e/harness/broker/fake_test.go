package broker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/cloudflare"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

const (
	testRun   = "ab12cd"
	otherRun  = "zz99yy"
	testToken = "cf-token-secret-value"
	zoneID    = "zone1"
)

// fakeZone is an in-memory Cloudflare zone behind the real client.
type fakeZone struct {
	mu      sync.Mutex
	records map[string]cloudflare.Record
	next    int
	calls   int
}

func newFakeZone(t *testing.T) (*fakeZone, *cloudflare.Client) {
	t.Helper()
	z := &fakeZone{records: map[string]cloudflare.Record{}}
	srv := httptest.NewServer(z)
	t.Cleanup(srv.Close)
	c, err := cloudflare.New(testToken, cloudflare.AllowedZone, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return z, c
}

func (z *fakeZone) add(typ, name, content string) {
	z.mu.Lock()
	defer z.mu.Unlock()
	z.next++
	id := "r" + strconv.Itoa(z.next)
	z.records[id] = cloudflare.Record{ID: id, Type: typ, Name: name, Content: content}
}

func (z *fakeZone) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	z.mu.Lock()
	defer z.mu.Unlock()
	z.calls++
	if r.URL.Path == "/zones" {
		reply(w, []map[string]string{{"id": zoneID, "name": r.URL.Query().Get("name")}})
		return
	}
	id := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/zones/"+zoneID+"/dns_records"), "/")
	switch r.Method {
	case http.MethodGet:
		q := r.URL.Query()
		out := []cloudflare.Record{}
		for _, rec := range z.records {
			if (q.Get("type") == "" || q.Get("type") == rec.Type) && (q.Get("name") == "" || q.Get("name") == rec.Name) {
				out = append(out, rec)
			}
		}
		reply(w, out)
	case http.MethodPost:
		raw, _ := io.ReadAll(r.Body)
		var rec cloudflare.Record
		_ = json.Unmarshal(raw, &rec)
		z.next++
		rec.ID = "r" + strconv.Itoa(z.next)
		rec.Content = `"` + rec.Content + `"` // Cloudflare quotes TXT content
		z.records[rec.ID] = rec
		reply(w, rec)
	case http.MethodDelete:
		delete(z.records, id)
		reply(w, map[string]string{"id": id})
	}
}

func reply(w http.ResponseWriter, result any) {
	raw, _ := json.Marshal(map[string]any{"success": true, "result": result, "result_info": map[string]int{"total_pages": 1}})
	_, _ = w.Write(raw)
}

// fakeCloud records what the broker asked for.
type fakeCloud struct {
	mu      sync.Mutex
	adds    []string
	removes []string
	// block makes AddExtra wait for its context and report why it ended.
	block   bool
	started chan struct{}
	ended   chan error
}

func (c *fakeCloud) AddExtra(ctx context.Context, st *fleet.State, name, location string) (fleet.Node, error) {
	c.mu.Lock()
	c.adds = append(c.adds, name)
	c.mu.Unlock()
	if c.block {
		close(c.started)
		<-ctx.Done()
		c.ended <- ctx.Err()
		return fleet.Node{}, ctx.Err()
	}
	for _, n := range st.Extras {
		if n.Name == name {
			return fleet.Node{}, errors.New("duplicate reached the cloud")
		}
	}
	return fleet.Node{Name: name, PublicIP: "203.0.113.50", Location: location, ServerID: 50}, nil
}

func (c *fakeCloud) RemoveExtra(_ context.Context, st *fleet.State, name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.removes = append(c.removes, name)
	for _, n := range st.Extras {
		if n.Name == name {
			return nil
		}
	}
	return errors.New(name + " is not in the state the broker passed")
}

func (c *fakeCloud) AddCluster(_ context.Context, _ *fleet.State, name string) (fleet.Cluster, error) {
	return fleet.Cluster{Name: name, Env: "e2e-" + testRun + "-" + name, BaseDomain: "e2e-" + testRun + "-" + name + ".dbrsteting.bid"}, nil
}

func (c *fakeCloud) RemoveCluster(context.Context, *fleet.State, string) error { return nil }

// serve starts a broker for testRun and returns a client for it.
func serve(t *testing.T, cloud *fakeCloud) (*fakeZone, *Client, *Listener) {
	t.Helper()
	zone, cf := newFakeZone(t)
	dir, err := os.MkdirTemp("", "brk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	st := &fleet.State{RunID: testRun, Nodes: []fleet.Node{{Name: "node-1"}}, Extras: []fleet.Node{{Name: "extra-state"}}}
	srv := &Server{State: st, DNS: cf, Cloud: cloud, Logf: t.Logf,
		Redact: func(s string) string { return strings.ReplaceAll(s, testToken, "[REDACTED]") }}
	l, err := Listen(context.Background(), dir, srv, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := l.Close(); err != nil {
			t.Error(err)
		}
	})
	c, err := New(l.Path)
	if err != nil {
		t.Fatal(err)
	}
	return zone, c, l
}
