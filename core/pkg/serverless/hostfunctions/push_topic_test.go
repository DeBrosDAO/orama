package hostfunctions

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/migrations"
	"github.com/DeBrosOfficial/network/pkg/push"
	"github.com/DeBrosOfficial/network/pkg/rqlite/rqlitetest"
	"github.com/DeBrosOfficial/network/pkg/serverless"
	"go.uber.org/zap"
)

const (
	topicFnSecret = "a1a2a3a4a5a6a7a8a9aaabacadaeafb0b1b2b3b4b5b6b7b8b9babbbcbdbebfc0"
	topicFnToken  = "apns-device-token-for-topic"
)

// recordingProvider records the tokens it was asked to deliver to.
type recordingProvider struct {
	mu     sync.Mutex
	tokens []string
}

func (p *recordingProvider) Name() string { return "apns" }
func (p *recordingProvider) Send(_ context.Context, msg push.PushMessage) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.tokens = append(p.tokens, msg.DeviceToken)
	return nil
}

// newTopicHostFunctions wires a HostFunctions to a Manager with a real
// sqlite-backed topic store, and registers one topic in namespace "ns-a".
func newTopicHostFunctions(t *testing.T) (*HostFunctions, *recordingProvider, *sql.DB, string) {
	t.Helper()
	ddl, err := migrations.FS.ReadFile("059_push_topics.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	client, db := rqlitetest.SQLite(t, string(ddl))
	store, err := push.NewRqliteTopicStore(client, "hostfn-topic-test-ikm", "hostfn-topic-test-cluster-secret")
	if err != nil {
		t.Fatalf("topic store: %v", err)
	}
	provider := &recordingProvider{}
	h := &HostFunctions{pushManager: topicManager(store, provider)}

	topicID, err := push.TopicIDFromSecret(topicFnSecret)
	if err != nil {
		t.Fatalf("topic id: %v", err)
	}
	if _, err := store.Register(context.Background(), push.PushTopic{
		Namespace: "ns-a", TopicID: topicID, Provider: "apns", Token: topicFnToken,
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	return h, provider, db, topicID
}

// topicManager dispatches every namespace to provider (nil provider: push is
// not configured for any namespace).
func topicManager(store push.PushTopicStore, provider push.PushProvider) *push.Manager {
	factory := func(context.Context, push.Config) []push.PushProvider {
		if provider == nil {
			return nil
		}
		return []push.PushProvider{provider}
	}
	m := push.NewManager(nil, nil, push.Defaults{NtfyBaseURL: "http://ntfy"}, factory, zap.NewNop())
	m.SetTopicStore(store)
	return m
}

func decodeEnvelope(t *testing.T, raw []byte) push.SendDetailedResult {
	t.Helper()
	var res push.SendDetailedResult
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("envelope %s: %v", raw, err)
	}
	return res
}

func TestPushSendTopic_deliversInTheInvocationsNamespace(t *testing.T) {
	h, provider, _, topicID := newTopicHostFunctions(t)
	ctx := invocationCtx(&serverless.InvocationContext{Namespace: "ns-a"})

	raw, err := h.PushSendTopic(ctx, topicID, []byte(`{"title":"hi","body":"there"}`))
	if err != nil {
		t.Fatalf("PushSendTopic: %v", err)
	}
	res := decodeEnvelope(t, raw)
	if !res.Ok || res.DevicesAttempted != 1 || res.DevicesSucceeded != 1 {
		t.Errorf("envelope = %s, want one delivery", raw)
	}
	if len(provider.tokens) != 1 || provider.tokens[0] != topicFnToken {
		t.Errorf("provider tokens = %v, want [%s]", provider.tokens, topicFnToken)
	}
}

// The namespace comes from the invocation, so a function in another namespace
// cannot reach the topic even with its id.
func TestPushSendTopic_otherNamespaceIsTopicNotFound(t *testing.T) {
	h, provider, _, topicID := newTopicHostFunctions(t)
	ctx := invocationCtx(&serverless.InvocationContext{Namespace: "ns-b"})

	raw, err := h.PushSendTopic(ctx, topicID, []byte(`{"title":"hi"}`))
	if err != nil {
		t.Fatalf("PushSendTopic: %v", err)
	}
	assertTopicNotFound(t, raw)
	if len(provider.tokens) != 0 {
		t.Errorf("delivered across namespaces: %v", provider.tokens)
	}
}

func TestPushSendTopic_expiredTopicIsTopicNotFound(t *testing.T) {
	h, provider, db, topicID := newTopicHostFunctions(t)
	if _, err := db.Exec(`UPDATE push_topics SET expires_at = ?`, time.Now().Add(-time.Minute).Unix()); err != nil {
		t.Fatalf("expire: %v", err)
	}
	ctx := invocationCtx(&serverless.InvocationContext{Namespace: "ns-a"})

	raw, err := h.PushSendTopic(ctx, topicID, []byte(`{"title":"hi"}`))
	if err != nil {
		t.Fatalf("PushSendTopic: %v", err)
	}
	assertTopicNotFound(t, raw)
	if len(provider.tokens) != 0 {
		t.Errorf("delivered to an expired topic: %v", provider.tokens)
	}
}

func assertTopicNotFound(t *testing.T, raw []byte) {
	t.Helper()
	res := decodeEnvelope(t, raw)
	if res.Ok || res.DevicesAttempted != 0 || len(res.Results) != 1 {
		t.Fatalf("envelope = %s, want ok=false, nothing attempted, one result", raw)
	}
	if r := res.Results[0]; r.Reason != topicNotFoundReason || !r.Unregistered || r.Success {
		t.Errorf("result = %+v, want reason %s and unregistered", r, topicNotFoundReason)
	}
}

func TestPushSendTopic_validationFailuresAreErrors(t *testing.T) {
	h, provider, _, topicID := newTopicHostFunctions(t)
	withNS := invocationCtx(&serverless.InvocationContext{Namespace: "ns-a"})
	cases := []struct {
		name    string
		ctx     context.Context
		topicID string
		msg     string
	}{
		{"malformed topic id", withNS, "not-a-topic", `{"title":"hi"}`},
		{"uppercase topic id", withNS, strings.ToUpper(topicID), `{"title":"hi"}`},
		{"invalid json", withNS, topicID, `{"title":`},
		{"oversized message", withNS, topicID, `{"body":"` + strings.Repeat("x", MaxPushSendArgsBytes) + `"}`},
		{"no namespace", context.Background(), topicID, `{"title":"hi"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := h.PushSendTopic(c.ctx, c.topicID, []byte(c.msg))
			if err == nil {
				t.Fatalf("accepted; envelope %s", out)
			}
		})
	}
	if len(provider.tokens) != 0 {
		t.Errorf("a rejected call delivered: %v", provider.tokens)
	}
}

// The secret is what registers a topic; it does not address one. Passed as a
// topic id it is simply an id nobody registered.
func TestPushSendTopic_theSecretDoesNotAddressTheTopic(t *testing.T) {
	h, provider, _, _ := newTopicHostFunctions(t)
	ctx := invocationCtx(&serverless.InvocationContext{Namespace: "ns-a"})

	raw, err := h.PushSendTopic(ctx, topicFnSecret, []byte(`{"title":"hi"}`))
	if err != nil {
		t.Fatalf("PushSendTopic: %v", err)
	}
	assertTopicNotFound(t, raw)
	if len(provider.tokens) != 0 {
		t.Errorf("the secret addressed the topic: %v", provider.tokens)
	}
}

func TestPushSendTopic_noPushConfiguredIsNoOp(t *testing.T) {
	h := &HostFunctions{}
	raw, err := h.PushSendTopic(context.Background(), "irrelevant", []byte(`{}`))
	if err != nil {
		t.Fatalf("PushSendTopic: %v", err)
	}
	if string(raw) != pushNotConfiguredEnvelope {
		t.Errorf("envelope = %s, want the not-configured no-op", raw)
	}
}

// A namespace with no push provider gets the not-configured envelope, not an
// error, exactly as push_send_v2 does.
func TestPushSendTopic_namespaceWithoutProviderIsNoOp(t *testing.T) {
	h, _, _, topicID := newTopicHostFunctions(t)
	h.pushManager = topicManager(h.pushManager.TopicStore(), nil)

	raw, err := h.PushSendTopic(invocationCtx(&serverless.InvocationContext{Namespace: "ns-a"}), topicID, []byte(`{"title":"hi"}`))
	if err != nil {
		t.Fatalf("PushSendTopic: %v", err)
	}
	if string(raw) != pushNotConfiguredEnvelope {
		t.Errorf("envelope = %s, want the not-configured no-op", raw)
	}
}

// failingTopicStore fails every lookup, as an unreachable database would.
type failingTopicStore struct{ push.PushTopicStore }

func (failingTopicStore) Get(context.Context, string, string) (*push.PushTopic, error) {
	return nil, errors.New("rqlite: leader unavailable")
}

// A store failure is a failed call (0 to the guest), never a TopicNotFound
// that would tell the caller to drop a topic that may be fine.
func TestPushSendTopic_storeFailureIsAnError(t *testing.T) {
	provider := &recordingProvider{}
	h := &HostFunctions{pushManager: topicManager(failingTopicStore{}, provider)}
	topicID, _ := push.TopicIDFromSecret(topicFnSecret)

	out, err := h.PushSendTopic(invocationCtx(&serverless.InvocationContext{Namespace: "ns-a"}), topicID, []byte(`{"title":"hi"}`))
	if err == nil {
		t.Fatalf("store failure returned an envelope: %s", out)
	}
	if len(provider.tokens) != 0 {
		t.Errorf("delivered despite the failure: %v", provider.tokens)
	}
}
