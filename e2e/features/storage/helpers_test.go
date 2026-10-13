//go:build e2e_fleet

package storage

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

// Routes (docs/whitepaper/technical-reference/appendices/i-api-surface.md#storage) and limits
// (core/pkg/gateway/handlers/storage).
const (
	pathUpload = "/v1/storage/upload"
	pathPin    = "/v1/storage/pin"
	pathGet    = "/v1/storage/get/"
	pathStatus = "/v1/storage/status/"
	pathUnpin  = "/v1/storage/unpin/"
	// jsonBodyLimit is the JSON upload's body cap (1 MiB, upload_handler.go).
	jsonBodyLimit = 1 << 20
	// replicationFactor is the cluster default (namespace cluster_manager.go).
	replicationFactor = 3
	// pinPropagation is the window a fresh pin may still be invisible on a
	// node (download_handler.go pinPropagationWindow) plus slack.
	pinPropagation = 3 * time.Minute
	pollEvery      = 2 * time.Second
	pinnedStatus   = "pinned"
	wrapMagic      = "ORMAW1" // core/pkg/ipfs/wrap.go
	codeJWTNeeded  = "USER_JWT_REQUIRED"
)

type uploaded struct {
	Cid  string `json:"cid"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

type pinStatus struct {
	Cid               string   `json:"cid"`
	Name              string   `json:"name"`
	Status            string   `json:"status"`
	ReplicationFactor int      `json:"replication_factor"`
	Peers             []string `json:"peers"`
	Error             string   `json:"error"`
}

// randomBytes is n random bytes; content is unique per test so a CID is too.
func randomBytes(t testing.TB, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

// multipartBody is a form with the file part and, when pin != "", the pin field.
func multipartBody(t testing.TB, name string, data []byte, pin string) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if pin != "" {
		if err := w.WriteField("pin", pin); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes(), w.FormDataContentType()
}

// uploadRaw sends a multipart upload and returns the raw answer.
func uploadRaw(t testing.TB, c *gw.Client, who tenancy.Cred, name string, data []byte) *gw.Response {
	t.Helper()
	body, ct := multipartBody(t, name, data, "")
	return c.MustSend(t, gw.Req{Method: http.MethodPost, Path: pathUpload, Bearer: who.Bearer, APIKey: who.APIKey,
		Header: http.Header{"Content-Type": {ct}}, Body: body})
}

// upload stores data as name and returns what the gateway answered.
func upload(t testing.TB, c *gw.Client, who tenancy.Cred, name string, data []byte) uploaded {
	t.Helper()
	var u uploaded
	if err := uploadRaw(t, c, who, name, data).Expect(t, http.StatusOK).Decode(&u); err != nil {
		t.Fatal(err)
	}
	if u.Cid == "" {
		t.Fatalf("upload of %s returned no CID", name)
	}
	return u
}

// uploadJSON is the SDK's JSON form: {name, data: base64}.
func uploadJSON(t testing.TB, c *gw.Client, who tenancy.Cred, name string, data []byte) *gw.Response {
	t.Helper()
	return tenancy.Post(t, c, pathUpload, who, map[string]string{"name": name, "data": base64.StdEncoding.EncodeToString(data)})
}

// get downloads cid once.
func get(t testing.TB, c *gw.Client, who tenancy.Cred, cid string) *gw.Response {
	t.Helper()
	return tenancy.Get(t, c, pathGet+cid, who)
}

// waitContent polls until cid downloads as want: a fresh pin may be invisible
// on a node for up to two minutes (404, retryable).
func waitContent(t testing.TB, c *gw.Client, who tenancy.Cred, cid string, want []byte) {
	t.Helper()
	eventually.Require(t, pollEvery, pinPropagation, "download of "+cid, func() (bool, error) {
		r := get(t, c, who, cid)
		if r.Status != http.StatusOK {
			return false, fmt.Errorf("status %d: %.200s", r.Status, r.Body)
		}
		if !bytes.Equal(r.Body, want) {
			return false, eventually.Stop(fmt.Errorf("downloaded %d bytes that differ from the %d uploaded", len(r.Body), len(want)))
		}
		return true, nil
	})
}

// status reads the pin status of cid.
func status(t testing.TB, c *gw.Client, who tenancy.Cred, cid string) (*gw.Response, pinStatus) {
	t.Helper()
	var s pinStatus
	r := tenancy.Get(t, c, pathStatus+cid, who)
	if r.Status == http.StatusOK {
		if err := json.Unmarshal(r.Body, &s); err != nil {
			t.Fatalf("status of %s: %v", cid, err)
		}
	}
	return r, s
}

// waitPinned polls until cid is pinned on every replica.
func waitPinned(t testing.TB, c *gw.Client, who tenancy.Cred, cid string) pinStatus {
	t.Helper()
	var last pinStatus
	eventually.Require(t, pollEvery, pinPropagation, cid+" pinned on "+fmt.Sprint(replicationFactor)+" peers", func() (bool, error) {
		r, s := status(t, c, who, cid)
		if r.Status != http.StatusOK {
			return false, fmt.Errorf("status %d: %.200s", r.Status, r.Body)
		}
		last = s
		if s.Status != pinnedStatus || len(s.Peers) < replicationFactor {
			return false, fmt.Errorf("status %q on %d peers", s.Status, len(s.Peers))
		}
		return true, nil
	})
	return last
}

// unpin sends DELETE /v1/storage/unpin/<cid>[?immediate=true].
func unpin(t testing.TB, c *gw.Client, who tenancy.Cred, cid string, immediate bool) (*gw.Response, map[string]any) {
	t.Helper()
	req := gw.Req{Method: http.MethodDelete, Path: pathUnpin + cid, Bearer: who.Bearer, APIKey: who.APIKey}
	if immediate {
		req.Query = map[string][]string{"immediate": {"true"}}
	}
	r := c.MustSend(t, req)
	var body map[string]any
	if r.Status == http.StatusOK {
		if err := json.Unmarshal(r.Body, &body); err != nil {
			t.Fatalf("unpin answer: %v", err)
		}
	}
	return r, body
}
