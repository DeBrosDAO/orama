//go:build e2e_fleet

package pubsub

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

// Close codes of a socket whose credential stopped authorizing it
// (core/pkg/gateway/wssession/registry.go).
const (
	closeRevoked = 4403
	// sweepBudget: every open socket is re-checked every 10s against a list
	// reloaded at each sweep (docs/AUTH.md#revoking), plus the round trip.
	sweepBudget = 25 * time.Second
)

func TestPubsubAuth_noCredentialRefused(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	tenancy.ExpectRefused(t, publish(t, n.Client, tenancy.Cred{}, "t", []byte("x")), http.StatusUnauthorized, tenancy.CodeMissing)
	tenancy.ExpectRefused(t, tenancy.Post(t, n.Client, pathBatch, tenancy.Cred{}, batch("t", "x")), http.StatusUnauthorized, tenancy.CodeMissing)
	tenancy.ExpectRefused(t, tenancy.Send(t, n.Client, http.MethodGet, pathTopics, tenancy.Cred{}, nil), http.StatusUnauthorized, tenancy.CodeMissing)
	tenancy.ExpectRefused(t, tenancy.Send(t, n.Client, http.MethodGet, pathPresence+"?topic=t", tenancy.Cred{}, nil), http.StatusUnauthorized, tenancy.CodeMissing)
	if _, resp, err := dial(t, n.Client, "", pathWS+"?topic=t", tenancy.Cred{}); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("an anonymous subscribe was not refused with 401: %v %v", statusOf(resp), err)
	}
}

// TestPubsubAuth_rolesAndKeysDecide: runtime and a pubsub key publish and
// subscribe; reader and a cache-only key do neither
// (docs/CLI_REFERENCE.md "orama members", docs/ARCHITECTURE.md "API Keys").
func TestPubsubAuth_rolesAndKeysDecide(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	allowed := map[string]tenancy.Cred{
		"runtime member": {Bearer: tenancy.Member(t, n, tenancy.RoleRuntime).Token()},
		"pubsub key":     {APIKey: tenancy.APIKey(t, n, "pubsub")},
	}
	denied := map[string]tenancy.Cred{
		"reader member": {Bearer: tenancy.Member(t, n, tenancy.RoleReader).Token()},
		"cache key":     {APIKey: tenancy.APIKey(t, n, "cache")},
	}
	for name, who := range allowed {
		publish(t, n.Client, who, "t", []byte(name)).Expect(t, http.StatusOK)
		subscribe(t, n.Client, "", "t", who, nil)
	}
	for name, who := range denied {
		tenancy.ExpectRefused(t, publish(t, n.Client, who, "t", []byte(name)), http.StatusForbidden, tenancy.CodeScope)
		if _, resp, err := dial(t, n.Client, "", pathWS+"?topic=t", who); err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s: subscribe was not refused with 403: %v %v", name, statusOf(resp), err)
		}
	}
}

// TestPubsubAuth_revokedSessionClosesSocket: signing a member out closes its
// open socket with 4403 within one sweep (core/pkg/gateway/wssession), and
// nothing is delivered to it afterwards.
func TestPubsubAuth_revokedSessionClosesSocket(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	m := tenancy.Member(t, n, tenancy.RoleRuntime)
	node := f.State.Nodes[0]
	c := n.Client.PinTo(node.PublicIP)
	s := subscribe(t, c, node.PublicIP, "private", tenancy.Cred{Bearer: m.Token()}, nil)
	warmUp(t, c, tenancy.Owner(n), s, "private")
	if _, err := m.Client.For(t).Logout(t.Context(), m.Token(), m.Session.RefreshToken, n.Name, false); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(sweepBudget)
	for time.Now().Before(deadline) {
		if _, ok := s.next(t, time.Until(deadline)); !ok {
			break
		}
	}
	var err error
	select {
	case err = <-s.closed:
	default:
		t.Fatalf("the revoked session's socket is still open after %s", sweepBudget)
	}
	var ce *websocket.CloseError
	if !errors.As(err, &ce) || ce.Code != closeRevoked {
		t.Fatalf("the revoked session's socket was not closed with %d within %s: %v", closeRevoked, sweepBudget, err)
	}
	// This node's revocation list refreshes on its own schedule (up to
	// sweepBudget), so a dial right after the close may still be let in: wait
	// for the refusal.
	eventually.Require(t, pollEvery, sweepBudget, "the revoked token to be refused a new socket", func() (bool, error) {
		conn, resp, derr := dial(t, c, node.PublicIP, pathWS+"?topic=private", tenancy.Cred{Bearer: m.Token()})
		if derr == nil {
			_ = conn.Close()
			return false, fmt.Errorf("the revoked token opened a new socket (HTTP %v)", statusOf(resp))
		}
		return true, nil
	})
}

// TestPubsubIsolation_topicsAreNamespaced: B publishing on a topic of the same
// name reaches nobody in A; B's credentials cannot publish into or subscribe
// to A (docs/SECURITY.md, NAMESPACE_MISMATCH).
func TestPubsubIsolation_topicsAreNamespaced(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	pair := tenancy.Namespaces(t, f, 2, ns.Options{})
	a, b := pair[0], pair[1]
	node := f.State.Nodes[0]
	ca, cb := a.Client.PinTo(node.PublicIP), b.Client.PinTo(node.PublicIP)
	s := subscribe(t, ca, node.PublicIP, "chat", tenancy.Owner(a), nil)
	warmUp(t, ca, tenancy.Owner(a), s, "chat")
	publish(t, cb, tenancy.Owner(b), "chat", []byte("from-b")).Expect(t, http.StatusOK)
	tenancy.ExpectDenied(t, publish(t, ca, tenancy.Owner(b), "chat", []byte("b-into-a")), "B's session publishing at A's gateway")
	tenancy.ExpectRefused(t, publish(t, ca, tenancy.Cred{APIKey: tenancy.APIKey(t, b, "pubsub")}, "chat", []byte("b-key-into-a")), http.StatusForbidden, tenancy.CodeMismatch)
	if _, resp, err := dial(t, ca, node.PublicIP, pathWS+"?topic=chat", tenancy.Owner(b)); err == nil {
		t.Fatalf("B's session subscribed at A's gateway (HTTP %v)", statusOf(resp))
	}
	publish(t, ca, tenancy.Owner(a), "chat", []byte("from-a")).Expect(t, http.StatusOK)
	// B's frames could arrive before or after A's own, so listen for a while
	// after it too.
	seen := s.await(t, []byte("from-a"), deliveryBudget)
	seen = append(seen, s.settle(t, settleWindow)...)
	for _, fr := range seen {
		if d := string(fr.Data); d == "from-b" || d == "b-into-a" || d == "b-key-into-a" {
			t.Fatalf("A's subscriber received %q from B", fr.Data)
		}
	}
}

// TestPubsubInput_topicsAreLiteral: a topic is a name, not a pattern:
// subscribing to "chat.*" does not receive "chat.a", and hostile names are
// just names.
func TestPubsubInput_topicsAreLiteral(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	node := f.State.Nodes[0]
	c := n.Client.PinTo(node.PublicIP)
	star := subscribe(t, c, node.PublicIP, "chat.*", tenancy.Owner(n), nil)
	warmUp(t, c, tenancy.Owner(n), star, "chat.*")
	publish(t, c, tenancy.Owner(n), "chat.a", []byte("specific")).Expect(t, http.StatusOK)
	publish(t, c, tenancy.Owner(n), "chat.*", []byte("literal")).Expect(t, http.StatusOK)
	for _, fr := range star.await(t, []byte("literal"), deliveryBudget) {
		if string(fr.Data) == "specific" {
			t.Fatal("a subscription to chat.* received a message on chat.a")
		}
	}
	for _, topic := range []string{"../../etc", "ns::other::x", "тема 話題", "\u202eevil", strings.Repeat("t", 1024)} {
		s := subscribe(t, c, node.PublicIP, topic, tenancy.Owner(n), nil)
		warmUp(t, c, tenancy.Owner(n), s, topic)
	}
}

func TestPubsubInput_malformedRefused(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	cases := map[string][]byte{
		"not json":      []byte("topic=t"),
		"missing topic": []byte(`{"data_base64":"eA=="}`),
		"missing data":  []byte(`{"topic":"t"}`),
		"bad base64":    []byte(`{"topic":"t","data_base64":"%%%"}`),
		"topic not str": []byte(`{"topic":5,"data_base64":"eA=="}`),
	}
	for name, body := range cases {
		if resp := tenancy.Post(t, n.Client, pathPublish, tenancy.Owner(n), body); resp.Status != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d", name, resp.Status)
		}
	}
	tenancy.Send(t, n.Client, http.MethodGet, pathPublish, tenancy.Owner(n), nil).Expect(t, http.StatusMethodNotAllowed)
	tenancy.Send(t, n.Client, http.MethodGet, pathBatch, tenancy.Owner(n), nil).Expect(t, http.StatusMethodNotAllowed)
	if _, resp, err := dial(t, n.Client, "", pathWS, tenancy.Owner(n)); err == nil || resp == nil || resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("a subscribe with no topic: want 400, got %v %v", statusOf(resp), err)
	}
}

// TestPubsubAuth_topicSelectorGrant: a member grant narrowed to
// pubsub:topic=chat.* reaches only matching topics when enforced, and nothing
// while the gateway reports it unenforced (docs/SECURITY.md "Resource
// selectors"; core/pkg/gateway/members_routes.go).
func TestPubsubAuth_topicSelectorGrant(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	sel := tenancy.SelectorMember(t, n, "pubsub:topic=chat.*")
	who := tenancy.Cred{Bearer: sel.Token}
	publish(t, n.Client, who, "billing", []byte("x")).Expect(t, http.StatusForbidden)
	if _, resp, err := dial(t, n.Client, "", pathWS+"?topic=billing", who); err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Errorf("a chat.* selector subscribed to billing: %v %v", statusOf(resp), err)
	}
	resp := publish(t, n.Client, who, "chat.room1", []byte("x"))
	switch {
	case sel.Enforced && resp.Status != http.StatusOK:
		t.Fatalf("an enforced chat.* selector could not publish to chat.room1: %d %s", resp.Status, resp.Body)
	case !sel.Enforced && resp.Status == http.StatusOK:
		t.Fatal("the gateway said the selector is not enforced, yet the grant published")
	}
}
