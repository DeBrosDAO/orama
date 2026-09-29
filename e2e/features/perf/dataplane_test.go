//go:build e2e_fleet

package perf

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"mime/multipart"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/realistic"
	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

// Load shapes and upper bounds. The bounds are generous: they catch a
// release that is broken-slow, the baseline comparison catches drift.
const (
	workers      = 8
	requests     = 300
	healthBound  = time.Second
	execBound    = 2 * time.Second
	queryBound   = 1500 * time.Millisecond
	cacheBound   = time.Second
	publishBound = time.Second
	uploads      = 30
	uploadSize   = 64 << 10
	uploadBound  = 5 * time.Second
	getBound     = 3 * time.Second
	pinVisible   = 3 * time.Minute // download_handler.go pinPropagationWindow plus slack
)

// jsonOp posts body to path on c as bearer and wants 200.
func jsonOp(c *gw.Client, path, bearer string, body func(i int) any) realistic.Op {
	return func(ctx context.Context, w, i int) error {
		_, err := c.JSON(ctx, http.MethodPost, path, bearer, body(w*requests+i), nil)
		return err
	}
}

// TestPerf_gatewayHealth: the public health route through DNS round robin.
func TestPerf_gatewayHealth(t *testing.T) {
	f := harness.Fleet(t)
	c := harness.GW(t)
	samples := realistic.Burst(t.Context(), workers, requests, func(ctx context.Context, _, _ int) error {
		return check(c.Send(ctx, gw.Req{Path: "/v1/health"}))
	})
	record(t, f, realistic.Summarize("gateway-health", samples, nil), healthBound)
}

// TestPerf_databaseExecAndQuery: single-row inserts, then point reads, on a
// namespace's RQLite through its gateway.
func TestPerf_databaseExecAndQuery(t *testing.T) {
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	tok := n.Owner.Token()
	if _, err := n.Client.JSON(t.Context(), http.MethodPost, "/v1/rqlite/create-table", tok,
		map[string]string{"schema": "CREATE TABLE perf (id INTEGER PRIMARY KEY, v TEXT NOT NULL)"}, nil); err != nil {
		t.Fatal(err)
	}
	exec := realistic.Burst(t.Context(), workers, requests, jsonOp(n.Client, "/v1/rqlite/exec", tok, func(i int) any {
		return map[string]any{"sql": "INSERT INTO perf (v) VALUES (?)", "args": []any{fmt.Sprintf("v%d", i)}}
	}))
	record(t, f, realistic.Summarize("db-exec", exec, nil), execBound)
	query := realistic.Burst(t.Context(), workers, requests, jsonOp(n.Client, "/v1/rqlite/query", tok, func(i int) any {
		return map[string]any{"sql": "SELECT v FROM perf WHERE id = ?", "args": []any{i%requests + 1}}
	}))
	record(t, f, realistic.Summarize("db-query", query, nil), queryBound)
}

// TestPerf_cachePutAndGet: puts then gets of the same keys in one dmap.
func TestPerf_cachePutAndGet(t *testing.T) {
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	tok := n.Owner.Token()
	put := realistic.Burst(t.Context(), workers, requests, jsonOp(n.Client, "/v1/cache/put", tok, func(i int) any {
		return map[string]any{"dmap": "perf", "key": fmt.Sprintf("k%d", i%requests), "value": map[string]any{"i": i}}
	}))
	record(t, f, realistic.Summarize("cache-put", put, nil), cacheBound)
	get := realistic.Burst(t.Context(), workers, requests, jsonOp(n.Client, "/v1/cache/get", tok, func(i int) any {
		return map[string]any{"dmap": "perf", "key": fmt.Sprintf("k%d", i%(requests/workers))}
	}))
	record(t, f, realistic.Summarize("cache-get", get, nil), cacheBound)
}

// TestPerf_pubsubPublish: publishes with no subscriber waiting on them.
func TestPerf_pubsubPublish(t *testing.T) {
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	data := base64.StdEncoding.EncodeToString([]byte(`{"perf":true}`))
	pub := realistic.Burst(t.Context(), workers, requests, jsonOp(n.Client, "/v1/pubsub/publish", n.Owner.Token(), func(int) any {
		return map[string]string{"topic": "perf", "data_base64": data}
	}))
	record(t, f, realistic.Summarize("pubsub-publish", pub, nil), publishBound)
}

// TestPerf_storageUploadAndGet: 64 KiB uploads, then downloads of them once
// each is visible everywhere.
func TestPerf_storageUploadAndGet(t *testing.T) {
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	tok := n.Owner.Token()
	var mu sync.Mutex
	var cids []string
	up := realistic.Burst(t.Context(), workers/2, uploads, func(ctx context.Context, _, _ int) error {
		cid, err := upload(ctx, n.Client, tok)
		if err == nil {
			mu.Lock()
			cids = append(cids, cid)
			mu.Unlock()
		}
		return err
	})
	record(t, f, realistic.Summarize("storage-upload", up, nil), uploadBound)
	for _, cid := range cids {
		eventually.Require(t, 2*time.Second, pinVisible, "download of "+cid, func() (bool, error) {
			err := check(n.Client.Send(t.Context(), gw.Req{Path: "/v1/storage/get/" + cid, Bearer: tok}))
			return err == nil, err
		})
	}
	get := realistic.Burst(t.Context(), workers, 3*len(cids), func(ctx context.Context, _, i int) error {
		return check(n.Client.Send(ctx, gw.Req{Path: "/v1/storage/get/" + cids[i%len(cids)], Bearer: tok}))
	})
	record(t, f, realistic.Summarize("storage-get", get, nil), getBound)
}

func upload(ctx context.Context, c *gw.Client, bearer string) (string, error) {
	data := make([]byte, uploadSize)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", "perf.bin")
	if err != nil {
		return "", err
	}
	if _, err := part.Write(data); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	r, err := c.Send(ctx, gw.Req{Method: http.MethodPost, Path: "/v1/storage/upload", Bearer: bearer,
		Header: http.Header{"Content-Type": {w.FormDataContentType()}}, Body: buf.Bytes()})
	if err := check(r, err); err != nil {
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
