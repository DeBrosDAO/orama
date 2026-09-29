//go:build e2e_fleet

package authkeysroles

import (
	"context"
	"encoding/base64"
	"net/http"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

const (
	pathPublish  = "/v1/pubsub/publish"
	pathCachePut = "/v1/cache/put"
	pathUpload   = "/v1/storage/upload"
	pathUnpin    = "/v1/storage/unpin/"
	pathQuery    = "/v1/rqlite/query"
)

func denied(resp *gw.Response) bool {
	return resp.Status == http.StatusForbidden
}

// TestGrants_pubsubTopicSelector: `pubsub:topic=chat.*` publishes to chat
// topics and nothing else, and never widens past pub/sub
// (docs/AUTH.md#narrowing-a-grant).
func TestGrants_pubsubTopicSelector(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{})
	c := harness.GW(t)
	_, tok := memberToken(t, n, roleRuntime, "pubsub:topic=chat.*")
	pub := func(topic string) *gw.Response {
		return send(t, c, http.MethodPost, pathPublish, tok, map[string]string{"topic": topic, "data_base64": "aGk="})
	}
	for _, topic := range []string{"chat.room", "chat.a.b"} {
		if r := pub(topic); denied(r) {
			t.Errorf("publishing to %s under chat.*: %d %s", topic, r.Status, r.Body)
		}
	}
	for _, topic := range []string{"orders", "chatroom", "x.chat.room"} {
		if r := pub(topic); !denied(r) {
			t.Errorf("publishing to %s under chat.*: want 403, got %d", topic, r.Status)
		}
	}
	if r := send(t, c, http.MethodPost, pathQuery, tok, map[string]string{"sql": "SELECT 1"}); !denied(r) {
		t.Errorf("a pub/sub-narrowed grant reached the database: %d", r.Status)
	}
}

// TestGrants_cacheKeySelector: `cache:key=sessions/*` matches <map>/<key>;
// a cache key is not a path, so sessions/../tokens/x is a key in sessions.
func TestGrants_cacheKeySelector(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{})
	c := harness.GW(t)
	_, tok := memberToken(t, n, roleRuntime, "cache:key=sessions/*")
	put := func(dmap, key string) *gw.Response {
		return send(t, c, http.MethodPost, pathCachePut, tok, map[string]string{"dmap": dmap, "key": key, "value": "v"})
	}
	for _, key := range []string{"abc", "../tokens/x", "a/b/c"} {
		if r := put("sessions", key); denied(r) {
			t.Errorf("put sessions/%s under sessions/*: %d %s", key, r.Status, r.Body)
		}
	}
	if r := put("tokens", "x"); !denied(r) {
		t.Errorf("put tokens/x under sessions/*: want 403, got %d", r.Status)
	}
}

// TestGrants_storageNameSelector: `storage:avatars/*` uploads under avatars/
// only, against the normalised name; `..` in a name is refused, not
// resolved (docs/AUTH.md#narrowing-a-grant).
func TestGrants_storageNameSelector(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{})
	c := harness.GW(t)
	_, tok := memberToken(t, n, roleRuntime, "storage:avatars/*")
	upload := func(name string) *gw.Response {
		data := base64.StdEncoding.EncodeToString([]byte("e2e " + name))
		r := send(t, c, http.MethodPost, pathUpload, tok, map[string]string{"name": name, "data": data})
		unpinAtCleanup(t, n, c, r)
		return r
	}
	for _, name := range []string{"avatars/me.png", "/avatars/2026/me.png", "avatars//deep/me.png"} {
		if r := upload(name); r.Status != http.StatusOK && r.Status != http.StatusCreated {
			t.Errorf("upload %q under avatars/*: %d %s", name, r.Status, r.Body)
		}
	}
	if r := upload("keys/x.png"); !denied(r) {
		t.Errorf("upload keys/x.png under avatars/*: want 403, got %d", r.Status)
	}
	for _, name := range []string{"avatars/../keys/x", "avatars/..", "../avatars/x"} {
		if r := upload(name); r.Status != http.StatusBadRequest {
			t.Errorf("upload %q: want 400 (a name is a label, .. is refused), got %d", name, r.Status)
		}
	}
}

// unpinAtCleanup unpins whatever an upload stored, as the namespace owner.
func unpinAtCleanup(t testing.TB, n *ns.Namespace, c *gw.Client, r *gw.Response) {
	t.Helper()
	var out struct {
		CID string `json:"cid"`
	}
	if r.Status/100 != 2 || r.Decode(&out) != nil || out.CID == "" {
		return
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), cleanupBudget)
		defer cancel()
		resp, err := c.Send(ctx, gw.Req{Method: http.MethodDelete, Path: pathUnpin + out.CID, Bearer: n.Owner.Token()})
		if err != nil || resp.Status/100 != 2 {
			t.Errorf("cleanup: failed to unpin %s: %v", out.CID, err)
		}
	})
}

// TestGrants_refusedAtWrite: a selector no data path applies (db, push), a
// domain that does not exist (deploy), one the role does not hold, and a
// narrowing on a reader are refused when written, not stored and ignored.
func TestGrants_refusedAtWrite(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{})
	cases := []struct{ role, resource string }{
		{roleAdmin, "db:table=posts"},
		{roleRuntime, "db:table=posts"},
		{roleRuntime, "push:topic=x"},
		{roleRuntime, "deploy:api"},
		{roleReader, "storage:avatars/*"},
		{roleRuntime, "storage:has space/*"},
		{roleRuntime, "nonsense"},
	}
	for _, tc := range cases {
		w := newWallet(t).Address()
		resp := grant(t, n, w, tc.role, tc.resource)
		if resp.Status != http.StatusBadRequest {
			t.Errorf("%s narrowed to %q: want 400, got %d %s", tc.role, tc.resource, resp.Status, resp.Body)
		}
		list := send(t, n.Owner.Client, http.MethodGet, pathMembers, n.Owner.Token(), nil).Expect(t, http.StatusOK)
		if containsFold(string(list.Body), w) {
			t.Errorf("a refused grant for %s was stored anyway", w)
		}
	}
}
