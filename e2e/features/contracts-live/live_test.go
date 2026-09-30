//go:build e2e_fleet

package contractslive

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/realistic"
	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
	"github.com/DeBrosOfficial/network/e2e/harness/wallet"
)

const (
	// torCheckURL answers through Tor; the proxy fixture's .onion is not live.
	torCheckURL = "https://check.torproject.org/api/ip"
	torBudget   = 3 * time.Minute
	pollEvery   = 5 * time.Second
	cleanupWait = time.Minute
)

// TestContractsAuth_liveShapesMatch: challenge, verify, refresh and logout
// as the fixtures send them, with a real wallet signature, against the
// public gateway; each answer has the fixture's shape.
func TestContractsAuth_liveShapesMatch(t *testing.T) {
	t.Parallel()
	fx := loadFixtures(t)
	c := harness.GW(t)
	w, err := wallet.NewEVM()
	if err != nil {
		t.Fatal(err)
	}
	ch := exchange(t, c, "", fx["auth/challenge"], map[string]any{"wallet": w.Address(), "namespace": gw.LobbyNamespace})
	msg, _ := ch["message"].(string)
	sig, err := w.Sign(msg)
	if err != nil {
		t.Fatal(err)
	}
	v := exchange(t, c, "", fx["auth/verify"], map[string]any{"message": msg, "signature": sig})
	access, refresh := protect(t, c, v)
	t.Cleanup(func() { logoutAll(t, c, &access) })
	r := exchange(t, c, "", fx["auth/refresh"], map[string]any{"refresh_token": refresh, "namespace": gw.LobbyNamespace})
	access, _ = protect(t, c, r)
	exchange(t, c, access, fx["auth/logout"], nil)
}

// protect registers the session's tokens with the redactor and returns them.
func protect(t testing.TB, c *gw.Client, session map[string]any) (access, refresh string) {
	t.Helper()
	access, _ = session["access_token"].(string)
	refresh, _ = session["refresh_token"].(string)
	for _, s := range []string{access, refresh} {
		if s == "" {
			t.Fatalf("the session lacks a token: %v", keysOf(session))
		}
		if err := c.Protect(s); err != nil {
			t.Fatal(err)
		}
	}
	return access, refresh
}

// logoutAll ends every session of the wallet; an already ended one is fine.
func logoutAll(t testing.TB, c *gw.Client, access *string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), cleanupWait)
	defer cancel()
	resp, err := c.Send(ctx, gw.Req{Method: http.MethodPost, Path: "/v1/auth/logout", Bearer: *access,
		Header: jsonHeader, Body: []byte(`{"all":true}`)})
	if err != nil || (resp.Status/100 != 2 && resp.Status != http.StatusUnauthorized) {
		t.Errorf("cleanup: logout: %v %v", err, resp)
	}
}

func keysOf(m map[string]any) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestContractsCache_liveShapesMatch: the cache fixtures, sent verbatim to a
// fresh namespace in the order an app uses them.
func TestContractsCache_liveShapesMatch(t *testing.T) {
	t.Parallel()
	fx := loadFixtures(t)
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	tok := n.Owner.Token()
	for _, name := range []string{"cache/put", "cache/get", "cache/scan", "cache/delete"} {
		exchange(t, n.Client, tok, fx[name], nil)
	}
}

// TestContractsDB_liveShapesMatch: the database fixtures, sent to a fresh
// namespace with the tables they name created first.
func TestContractsDB_liveShapesMatch(t *testing.T) {
	t.Parallel()
	fx := loadFixtures(t)
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	tok := n.Owner.Token()
	create := fx["db/create-table"]
	for _, schema := range []string{
		"CREATE TABLE messages (id INTEGER PRIMARY KEY AUTOINCREMENT, room TEXT, body TEXT)",
		"CREATE TABLE logs (id INTEGER PRIMARY KEY AUTOINCREMENT, msg TEXT)",
	} {
		exchange(t, n.Client, tok, create, map[string]any{"schema": schema})
	}
	exchange(t, n.Client, tok, create, nil)
	ex := exchange(t, n.Client, tok, fx["db/exec"], nil)
	exchange(t, n.Client, tok, fx["db/query"], nil)
	exchange(t, n.Client, tok, fx["db/find"], nil)
	exchange(t, n.Client, tok, fx["db/find-one"], map[string]any{"criteria": map[string]any{"id": ex["last_insert_id"]}})
	exchange(t, n.Client, tok, fx["db/transaction"], nil)
	exchange(t, n.Client, tok, fx["db/drop-table"], nil)
}

// TestContractsPubSub_publishLiveShapeMatches: the publish fixture verbatim.
func TestContractsPubSub_publishLiveShapeMatches(t *testing.T) {
	t.Parallel()
	fx := loadFixtures(t)
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	exchange(t, n.Client, n.Owner.Token(), fx["pubsub/publish"], nil)
}

// TestContractsStorage_pinLiveShapeMatches: the pin fixture with the CID of
// a file uploaded first; the pin is dropped at cleanup.
func TestContractsStorage_pinLiveShapeMatches(t *testing.T) {
	t.Parallel()
	fx := loadFixtures(t)
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	cid := upload(t, n)
	exchange(t, n.Client, n.Owner.Token(), fx["storage/pin"], map[string]any{"cid": cid})
}

// upload stores a small file through /v1/storage/upload and returns its CID;
// the cleanup unpins it.
func upload(t testing.TB, n *ns.Namespace) string {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", "avatar.png")
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(part, "e2e contracts %s", n.Name)
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	resp := n.Client.MustSend(t, gw.Req{Method: http.MethodPost, Path: "/v1/storage/upload", Bearer: n.Owner.Token(),
		Header: http.Header{"Content-Type": {mw.FormDataContentType()}}, Body: buf.Bytes()})
	var out struct {
		Cid string `json:"cid"`
	}
	if err := resp.Expect(t, http.StatusOK).Decode(&out); err != nil || out.Cid == "" {
		t.Fatalf("upload: %v %.200s", err, resp.Body)
	}
	t.Cleanup(func() {
		tenancy.Restore(t, n.Client, http.MethodDelete, "/v1/storage/unpin/"+out.Cid, tenancy.Owner(n), nil, http.StatusOK, http.StatusNotFound)
	})
	return out.Cid
}

// TestContractsProxy_anonLiveShapeMatches: the proxy fixture with a
// destination reachable through Tor (the fixture's .onion is a placeholder);
// Tor may still be building circuits, so the exchange is retried until the
// proxy answers 200, and the shape is checked once.
func TestContractsProxy_anonLiveShapeMatches(t *testing.T) {
	t.Parallel()
	fx := loadFixtures(t)
	fl := harness.Fleet(t)
	realistic.RequireReachableFromNode(t, fl, fl.State.Nodes[0], torCheckURL)
	n := tenancy.Namespace(t, fl, ns.Options{})
	f := fx["network/proxy-anon"]
	var body []byte
	eventually.Require(t, pollEvery, torBudget, "proxy answers through Tor", func() (bool, error) {
		resp := send(t, n.Client, n.Owner.Token(), f, map[string]any{"url": torCheckURL})
		body = resp.Body
		return resp.Status == http.StatusOK, fmt.Errorf("HTTP %d: %.200s", resp.Status, resp.Body)
	})
	checkShape(t, f, body)
}
