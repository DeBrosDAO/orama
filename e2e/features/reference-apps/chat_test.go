//go:build e2e_fleet

package referenceapps

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/realistic"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
)

const (
	chatUsers    = 4
	chatMessages = 3 // per user per round
	// meshBudget bounds a fresh subscription becoming reachable from a
	// publish on another node (GossipSub over the overlay).
	meshBudget     = 2 * time.Minute
	deliveryBudget = time.Minute
	pathPresence   = "/v1/pubsub/presence"
	chatNotify     = "chat-notify"
)

// chatMsg is what chat-send publishes on the room's topic.
type chatMsg struct {
	From string `json:"from"`
	Text string `json:"text"`
}

// member is one chat user with the presence socket they hold open.
type member struct {
	u    *realistic.User
	sock *realistic.Socket
	id   string
	mu   sync.Mutex
	seen []chatMsg
}

// chat is one room of the reference chat app.
type chat struct {
	tn      *realistic.Tenant
	room    string
	members []*member
}

// TestReferenceChat_anchatJourney is an AnChat-like session end to end: the
// chat front-end is served by name; four users sign in with wallets bound to
// device keys and hold presence sockets open on the room through different
// nodes; everyone is listed present; messages sent through the chat-send
// function carry the sender's wallet as the gateway authenticated it and
// reach every socket exactly once, in history too; an attachment uploaded by
// one user downloads byte for byte for another; a message that names a
// recipient is pushed to their phone through ntfy by a pubsub-triggered
// function; everybody's session refreshes with a device proof and the chat
// goes on; a user who leaves is announced and no longer listed.
func TestReferenceChat_anchatJourney(t *testing.T) {
	t.Parallel()
	realistic.RequireTinyGo(t)
	tn := realistic.NewTenant(t)
	web := tn.Deploy(t, "static", realistic.CopyApp(t, realistic.AppChatWeb, nil, nil), "chat")
	tn.EveryNodeServes(t, web, "/", "reference-chat")
	for name, role := range map[string]string{"chat-send": "chat", "chat-history": "chat", chatNotify: "relay", "chat-store": "store"} {
		tn.DeployFunction(t, name, role, false)
	}
	tn.N.CLI.MustOK(t, "function", "triggers", "add", chatNotify, "--topic", chatNotify)
	ch := joinRoom(t, tn, "room"+randomTopic(t)[6:14])
	ch.requirePresent(t, len(ch.members))
	ch.warmUp(t)
	ch.round(t, "round-1")
	ch.attachment(t)
	ch.push(t)
	for _, m := range ch.members {
		if err := m.u.Refresh(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	ch.round(t, "after-refresh")
	ch.leave(t)
}

// joinRoom signs the users in and opens each one's presence socket through
// node i mod the node count.
func joinRoom(t *testing.T, tn *realistic.Tenant, room string) *chat {
	t.Helper()
	ch := &chat{tn: tn, room: room}
	for i, u := range realistic.NewUsers(t, tn, roleRuntime, chatUsers) {
		node := tn.F.State.Nodes[i%len(tn.F.State.Nodes)]
		id := strings.ToLower(u.Wallet.Address()[2:12])
		q := url.Values{"presence": {"true"}, "member_id": {id}, "member_meta": {`{"device":"` + u.Device.ID()[:8] + `"}`}}
		s, err := realistic.Subscribe(t.Context(), tn.C.PinTo(node.PublicIP), ch.topic(), u.Token(), q)
		if err != nil {
			t.Fatalf("user %d on %s: %v", i, node.Name, err)
		}
		t.Cleanup(s.Close)
		ch.members = append(ch.members, &member{u: u, sock: s, id: id})
	}
	return ch
}

func (ch *chat) topic() string { return "chat." + ch.room }

// requirePresent waits until the room lists want members. A gateway lists
// the members whose socket it holds (core/pkg/gateway/handlers/pubsub
// presence_handler.go keeps presence in the process), so the room is the
// union of every node's list, with nobody listed twice.
func (ch *chat) requirePresent(t *testing.T, want int) {
	t.Helper()
	eventually.Require(t, pollEvery, deliveryBudget, fmt.Sprintf("%d members present", want), func() (bool, error) {
		seen := map[string]int{}
		for _, node := range ch.tn.F.State.Nodes {
			var out struct {
				Members []struct {
					MemberID string `json:"member_id"`
				} `json:"members"`
			}
			path := pathPresence + "?topic=" + url.QueryEscape(ch.topic())
			if _, err := getJSON(t.Context(), ch.tn.C.PinTo(node.PublicIP), path, ch.members[0].u.Token(), &out); err != nil {
				return false, err
			}
			for _, m := range out.Members {
				seen[m.MemberID]++
			}
		}
		for id, n := range seen {
			if n > 1 {
				return false, eventually.Stop(fmt.Errorf("%s is listed by %d gateways", id, n))
			}
		}
		return len(seen) == want, fmt.Errorf("%d present: %v", len(seen), seen)
	})
}

// send sends text through chat-send as m, naming notify (a wallet) to push.
func (ch *chat) send(t testing.TB, m *member, text, notify string) error {
	t.Helper()
	out, err := realistic.Invoke(t.Context(), ch.tn.C, "chat-send", m.u.Token(), map[string]string{"op": "send", "room": ch.room, "text": text, "notify": notify})
	if err != nil {
		return err
	}
	if from, _ := out["from"].(string); !strings.EqualFold(from, m.u.Subject()) {
		return fmt.Errorf("chat-send sent as %q, not as the caller %s", from, m.u.Subject())
	}
	return nil
}

// collect moves every message each socket received into its seen list.
func (ch *chat) collect() {
	for _, m := range ch.members {
		for _, f := range m.sock.Drain() {
			var msg chatMsg
			if json.Unmarshal(f.Data, &msg) == nil && msg.Text != "" {
				m.mu.Lock()
				m.seen = append(m.seen, msg)
				m.mu.Unlock()
			}
		}
	}
}

// count is how many times m has seen text.
func (m *member) count(text string) int {
	return m.countIf(func(s string) bool { return s == text })
}

// countPrefix is how many messages m has seen that start with prefix.
func (m *member) countPrefix(prefix string) int {
	return m.countIf(func(s string) bool { return strings.HasPrefix(s, prefix) })
}

func (m *member) countIf(match func(string) bool) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, s := range m.seen {
		if match(s.Text) {
			n++
		}
	}
	return n
}

// warmUp sends probes as the first member until every socket has one: new
// subscriptions take a moment to join the mesh other nodes publish into.
// This waits for readiness; the rounds assert on fresh messages.
func (ch *chat) warmUp(t *testing.T) {
	t.Helper()
	i := 0
	eventually.Require(t, pollEvery, meshBudget, "every socket reachable", func() (bool, error) {
		i++
		probe := fmt.Sprintf("warmup-%d", i)
		if err := ch.send(t, ch.members[0], probe, ""); err != nil {
			return false, eventually.Stop(err)
		}
		ch.collect()
		for j, m := range ch.members {
			if m.countPrefix("warmup-") == 0 {
				return false, fmt.Errorf("member %d has no probe yet", j)
			}
		}
		return true, nil
	})
}
