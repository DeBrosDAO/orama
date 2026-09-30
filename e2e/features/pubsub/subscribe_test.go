//go:build e2e_fleet

package pubsub

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"testing"

	"github.com/gorilla/websocket"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

// presenceEvent is what a presence join or leave publishes on the topic
// (subscribe_handler.go broadcastPresenceEvent).
type presenceEvent struct {
	Type     string         `json:"type"`
	MemberID string         `json:"member_id"`
	Meta     map[string]any `json:"meta"`
}

func presenceMembers(t testing.TB, n *ns.Namespace, c *clientPin, topic string) []string {
	t.Helper()
	var out struct {
		Members []struct {
			MemberID string `json:"member_id"`
		} `json:"members"`
		Count int `json:"count"`
	}
	resp := tenancy.Send(t, c.Client, http.MethodGet, pathPresence+"?topic="+url.QueryEscape(topic), tenancy.Owner(n), nil)
	if err := resp.Expect(t, http.StatusOK).Decode(&out); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, m := range out.Members {
		ids = append(ids, m.MemberID)
	}
	if out.Count != len(ids) {
		t.Errorf("presence count %d but %d members", out.Count, len(ids))
	}
	return ids
}

// TestPubsub_presenceJoinListLeave: a presence subscriber is listed with its
// member id, the others see presence.join and presence.leave, and the list
// empties when it goes (docs/TS_SDK.md "Pub/sub").
func TestPubsub_presenceJoinListLeave(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	node := f.State.Nodes[0]
	c := &clientPin{Client: n.Client.PinTo(node.PublicIP), IP: node.PublicIP}
	watcher := subscribe(t, c.Client, c.IP, "lobby", tenancy.Owner(n), nil)
	alice := subscribe(t, c.Client, c.IP, "lobby", tenancy.Owner(n),
		url.Values{"presence": {"true"}, "member_id": {"alice"}, "member_meta": {`{"role":"host"}`}})
	join := awaitPresence(t, watcher, "presence.join")
	if join.MemberID != "alice" || join.Meta["role"] != "host" {
		t.Errorf("join event %+v, want alice with meta role=host", join)
	}
	if ids := presenceMembers(t, n, c, "lobby"); !slices.Equal(ids, []string{"alice"}) {
		t.Fatalf("presence lists %v, want [alice]", ids)
	}
	if err := alice.conn.Close(); err != nil {
		t.Fatal(err)
	}
	if leave := awaitPresence(t, watcher, "presence.leave"); leave.MemberID != "alice" {
		t.Errorf("leave event %+v, want alice", leave)
	}
	eventually.Require(t, pollEvery, deliveryBudget, "presence to empty", func() (bool, error) {
		if ids := presenceMembers(t, n, c, "lobby"); len(ids) != 0 {
			return false, fmt.Errorf("still lists %v", ids)
		}
		return true, nil
	})
	if ids := presenceMembers(t, n, c, "never-used"); len(ids) != 0 {
		t.Fatalf("an unused topic lists %v", ids)
	}
}

func awaitPresence(t testing.TB, s *sub, typ string) presenceEvent {
	t.Helper()
	for {
		fr, ok := s.next(t, deliveryBudget)
		if !ok {
			t.Fatalf("no %s event within %s", typ, deliveryBudget)
		}
		var ev presenceEvent
		if json.Unmarshal(fr.Data, &ev) == nil && ev.Type == typ {
			return ev
		}
	}
}

func TestPubsub_presenceNeedsMemberID(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	_, resp, err := dial(t, n.Client, "", pathWS+"?topic=lobby&presence=true", tenancy.Owner(n))
	if err == nil || resp == nil || resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("presence without member_id: want a refused upgrade with 400, got %v %v", statusOf(resp), err)
	}
	tenancy.Send(t, n.Client, http.MethodGet, pathPresence, tenancy.Owner(n), nil).Expect(t, http.StatusBadRequest)
	tenancy.Send(t, n.Client, http.MethodPost, pathPresence+"?topic=lobby", tenancy.Owner(n), nil).Expect(t, http.StatusMethodNotAllowed)
}

// TestPubsub_topicsListsSubscribedTopics: /v1/pubsub/topics is the SDK's
// pubsub.topics() (docs/API_SURFACE.md "Pub/sub") and lists the namespace's
// subscribed topics, never another namespace's.
func TestPubsub_topicsListsSubscribedTopics(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	pair := tenancy.Namespaces(t, f, 2, ns.Options{})
	a, b := pair[0], pair[1]
	node := f.State.Nodes[0]
	ca, cb := a.Client.PinTo(node.PublicIP), b.Client.PinTo(node.PublicIP)
	subscribe(t, ca, node.PublicIP, "listed-a", tenancy.Owner(a), nil)
	subscribe(t, cb, node.PublicIP, "listed-b", tenancy.Owner(b), nil)
	var out struct {
		Topics []string `json:"topics"`
	}
	eventually.Require(t, pollEvery, deliveryBudget, "A's topic to be listed", func() (bool, error) {
		if err := tenancy.Send(t, ca, http.MethodGet, pathTopics, tenancy.Owner(a), nil).Expect(t, http.StatusOK).Decode(&out); err != nil {
			return false, eventually.Stop(err)
		}
		if !slices.Contains(out.Topics, "listed-a") {
			return false, fmt.Errorf("topics %v", out.Topics)
		}
		return true, nil
	})
	if slices.Contains(out.Topics, "listed-b") {
		t.Fatalf("A's topic list shows B's topic: %v", out.Topics)
	}
}

// TestPubsub_reconnectResumesWithoutReplay: a closed subscription receives
// nothing published while it was closed; a new one receives what follows.
func TestPubsub_reconnectResumesWithoutReplay(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	node := f.State.Nodes[0]
	c := n.Client.PinTo(node.PublicIP)
	first := subscribe(t, c, node.PublicIP, "resume", tenancy.Owner(n), nil)
	warmUp(t, c, tenancy.Owner(n), first, "resume")
	if err := first.conn.Close(); err != nil {
		t.Fatal(err)
	}
	publish(t, c, tenancy.Owner(n), "resume", []byte("while-away")).Expect(t, http.StatusOK)
	second := subscribe(t, c, node.PublicIP, "resume", tenancy.Owner(n), nil)
	warmUp(t, c, tenancy.Owner(n), second, "resume")
	publish(t, c, tenancy.Owner(n), "resume", []byte("after")).Expect(t, http.StatusOK)
	for _, fr := range second.await(t, []byte("after"), deliveryBudget) {
		if string(fr.Data) == "while-away" {
			t.Fatal("a message published while no socket was open was replayed")
		}
	}
}

// TestPubsub_oneSubscriberLeavingKeepsOthers: two sockets on the same gateway
// share a topic; when one closes, the other keeps receiving messages
// published through another node.
func TestPubsub_oneSubscriberLeavingKeepsOthers(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	nodes := tenancy.PerNode(t, f, n.Client)
	c := &clientPin{Client: nodes[0].Client, IP: nodes[0].Node.PublicIP}
	stays := subscribe(t, c.Client, c.IP, "shared", tenancy.Owner(n), url.Values{"presence": {"true"}, "member_id": {"stays"}})
	leaves := subscribe(t, c.Client, c.IP, "shared", tenancy.Owner(n), url.Values{"presence": {"true"}, "member_id": {"leaves"}})
	warmUp(t, nodes[1].Client, tenancy.Owner(n), stays, "shared")
	leaves.drain(t)
	if err := leaves.conn.Close(); err != nil {
		t.Fatal(err)
	}
	// Presence shows the gateway has run the leaver's unsubscribe, so the
	// message below can only reach the other socket if leaving kept it.
	eventually.Require(t, pollEvery, deliveryBudget, "the gateway to drop the closed socket", func() (bool, error) {
		if ids := presenceMembers(t, n, c, "shared"); !slices.Equal(ids, []string{"stays"}) {
			return false, fmt.Errorf("presence lists %v, want [stays]", ids)
		}
		return true, nil
	})
	msg := []byte("still-here")
	publish(t, nodes[1].Client, tenancy.Owner(n), "shared", msg).Expect(t, http.StatusOK)
	stays.await(t, msg, deliveryBudget)
}

// TestPubsub_socketPublish: a text frame written on the subscription socket
// is published to the topic (subscribe_handler.go readerLoop); a
// {"type":"ping"} heartbeat is not.
func TestPubsub_socketPublish(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	nodes := tenancy.PerNode(t, f, n.Client)
	listener := subscribe(t, nodes[1].Client, nodes[1].Node.PublicIP, "talk", tenancy.Owner(n), nil)
	talker := subscribe(t, nodes[0].Client, nodes[0].Node.PublicIP, "talk", tenancy.Owner(n), nil)
	warmUp(t, nodes[0].Client, tenancy.Owner(n), listener, "talk")
	for _, m := range [][]byte{[]byte(`{"type":"ping"}`), []byte("spoken")} {
		if err := talker.conn.WriteMessage(websocket.TextMessage, m); err != nil {
			t.Fatal(err)
		}
	}
	for _, fr := range listener.await(t, []byte("spoken"), deliveryBudget) {
		if string(fr.Data) == `{"type":"ping"}` {
			t.Fatal("a heartbeat ping was published as a message")
		}
	}
}

// clientPin is a namespace client pinned to one node, with that node's IP for
// WebSocket dials.
type clientPin struct {
	Client *gw.Client
	IP     string
}
