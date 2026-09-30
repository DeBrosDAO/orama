//go:build e2e_fleet

package soak

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"mime/multipart"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/realistic"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

const (
	soakUsers   = 4
	roleRuntime = "runtime"
	roleAdmin   = "admin"
	blobSize    = 16 << 10
	siteMarker  = "soak-site"
	// pinVisible: a fresh pin may be invisible on a node for two minutes
	// (storage download_handler.go pinPropagationWindow).
	pinVisible = 3 * time.Minute
)

// workload is the soak's tenant and its simulated users.
type workload struct {
	tn    *realistic.Tenant
	users []*realistic.User
	// admin holds the database (raw rqlite is the admin grant's).
	admin *realistic.User
	site  string
	cid   string
	blob  []byte
	pub   *gw.Client // the public gateway, quiet
	ns    *gw.Client // the namespace gateway, quiet
	app   *gw.Client // the static site by name, quiet
	seq   atomic.Int64
}

// setupWorkload creates the tenant, its users, the site, the table and one
// stored object every download reads.
func setupWorkload(t *testing.T) *workload {
	t.Helper()
	tn := realistic.NewTenant(t)
	w := &workload{tn: tn, users: realistic.NewUsers(t, tn, roleRuntime, soakUsers), admin: realistic.NewUsers(t, tn, roleAdmin, 1)[0]}
	w.site = tn.Deploy(t, "static", realistic.CopyApp(t, realistic.AppStatic, map[string]string{realistic.ReleaseMarker: siteMarker}, nil), "soaksite")
	tn.EveryNodeServes(t, w.site, "/", siteMarker)
	w.pub = realistic.Quiet(t, tn.F, tn.F.State.GatewayURL)
	w.ns = realistic.Quiet(t, tn.F, tn.C.BaseURL)
	w.app = realistic.Quiet(t, tn.F, w.site)
	if _, err := tn.C.JSON(t.Context(), http.MethodPost, "/v1/rqlite/create-table", w.admin.Token(),
		map[string]string{"schema": "CREATE TABLE soak (k TEXT PRIMARY KEY, v INTEGER NOT NULL)"}, nil); err != nil {
		t.Fatal(err)
	}
	w.blob = make([]byte, blobSize)
	if _, err := rand.Read(w.blob); err != nil {
		t.Fatal(err)
	}
	cid, err := w.upload(t.Context(), w.users[0].Token())
	if err != nil {
		t.Fatal(err)
	}
	w.cid = cid
	eventually.Require(t, realistic.PollEvery, pinVisible, "the stored object downloadable", func() (bool, error) {
		err := ok(tn.C.Send(t.Context(), gw.Req{Path: "/v1/storage/get/" + cid, Bearer: w.users[0].Token()}))
		return err == nil, err
	})
	return w
}

// op is one kind of traffic: its load, and how often each worker calls it.
type op struct {
	name     string
	workers  int
	interval time.Duration
	call     realistic.Op
}

// ops is the mixed traffic every user generates.
func (w *workload) ops() []op {
	return []op{
		{"health", 2, time.Second, func(ctx context.Context, _, _ int) error { return ok(w.pub.Send(ctx, gw.Req{Path: "/v1/health"})) }},
		{"site", 2, time.Second, func(ctx context.Context, _, i int) error {
			return ok(w.app.Send(ctx, gw.Req{Path: []string{"/", "/pricing.html", "/assets/site.css"}[i%3]}))
		}},
		{"cache", 2, time.Second, w.cacheRoundTrip},
		{"db", 2, 2 * time.Second, w.dbRoundTrip},
		{"storage-get", 1, 5 * time.Second, func(ctx context.Context, wk, _ int) error {
			return ok(w.ns.Send(ctx, gw.Req{Path: "/v1/storage/get/" + w.cid, Bearer: w.user(wk).Token()}))
		}},
		{"storage-upload", 1, 30 * time.Second, func(ctx context.Context, wk, _ int) error {
			_, err := w.upload(ctx, w.user(wk).Token())
			return err
		}},
		{"refresh", soakUsers, time.Minute, func(ctx context.Context, wk, _ int) error { return w.users[wk].Refresh(ctx) }},
		{"admin-refresh", 1, time.Minute, func(ctx context.Context, _, _ int) error { return w.admin.Refresh(ctx) }},
	}
}

func (w *workload) user(worker int) *realistic.User { return w.users[worker%len(w.users)] }

// cacheRoundTrip writes a value and reads it back.
func (w *workload) cacheRoundTrip(ctx context.Context, wk, i int) error {
	key, want := fmt.Sprintf("w%d-%d", wk, i%50), fmt.Sprintf("v%d", i)
	tok := w.user(wk).Token()
	if _, err := w.ns.JSON(ctx, http.MethodPost, "/v1/cache/put", tok, map[string]any{"dmap": "soak", "key": key, "value": want}, nil); err != nil {
		return err
	}
	var got struct {
		Value any `json:"value"`
	}
	if _, err := w.ns.JSON(ctx, http.MethodPost, "/v1/cache/get", tok, map[string]any{"dmap": "soak", "key": key}, &got); err != nil {
		return err
	}
	if got.Value != want {
		return fmt.Errorf("cache %s read %v, want %s", key, got.Value, want)
	}
	return nil
}

// dbRoundTrip upserts a row and reads it back.
func (w *workload) dbRoundTrip(ctx context.Context, wk, i int) error {
	key, v := fmt.Sprintf("w%d-%d", wk, i%100), w.seq.Add(1)
	tok := w.admin.Token()
	if _, err := w.ns.JSON(ctx, http.MethodPost, "/v1/rqlite/exec", tok, map[string]any{"sql": "INSERT OR REPLACE INTO soak (k, v) VALUES (?, ?)", "args": []any{key, v}}, nil); err != nil {
		return err
	}
	var out struct {
		Items []map[string]any `json:"items"`
	}
	if _, err := w.ns.JSON(ctx, http.MethodPost, "/v1/rqlite/query", tok, map[string]any{"sql": "SELECT v FROM soak WHERE k = ?", "args": []any{key}}, &out); err != nil {
		return err
	}
	if len(out.Items) != 1 {
		return fmt.Errorf("db %s read %d rows", key, len(out.Items))
	}
	return nil
}

// upload stores the blob as bearer and returns its CID.
func (w *workload) upload(ctx context.Context, bearer string) (string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", "soak.bin")
	if err != nil {
		return "", err
	}
	if _, err := part.Write(w.blob); err != nil {
		return "", err
	}
	if err := mw.Close(); err != nil {
		return "", err
	}
	r, err := w.ns.Send(ctx, gw.Req{Method: http.MethodPost, Path: "/v1/storage/upload", Bearer: bearer,
		Header: http.Header{"Content-Type": {mw.FormDataContentType()}}, Body: buf.Bytes()})
	if err := ok(r, err); err != nil {
		return "", err
	}
	var out struct {
		Cid string `json:"cid"`
	}
	if err := r.Decode(&out); err != nil || out.Cid == "" {
		return "", fmt.Errorf("upload answered no CID: %v", err)
	}
	return out.Cid, nil
}

// ok is an operation error unless the answer is 200.
func ok(r *gw.Response, err error) error {
	if err != nil {
		return err
	}
	if r.Status != http.StatusOK {
		return fmt.Errorf("HTTP %d %.120s", r.Status, r.Body)
	}
	return nil
}
