package push

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/migrations"
	authsvc "github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/push"
	"github.com/DeBrosOfficial/network/pkg/rqlite/rqlitetest"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

const (
	topicNS      = "anchat"
	topicWallet  = "0xCALLERWALLETSUBJECT0000000000000000000001"
	topicAccount = "account-root-id-0001"
	secretA      = "0a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20212223242526272829"
	secretB      = "f0f1f2f3f4f5f6f7f8f9fafbfcfdfeff000102030405060708090a0b0c0d0e0f"
	tokenA       = "apns-token-device-a"
	tokenB       = "apns-token-device-b"
)

// topicEnv is a Handlers wired to a Manager with a real sqlite topic store,
// a recording provider and a log observer.
type topicEnv struct {
	h     *Handlers
	db    *sql.DB
	logs  *observer.ObservedLogs
	sends []string
}

func newTopicEnv(t *testing.T) *topicEnv {
	t.Helper()
	ddl, err := migrations.FS.ReadFile("059_push_topics.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	client, db := rqlitetest.SQLite(t, string(ddl))
	store, err := push.NewRqliteTopicStore(client, "handler-topic-test-ikm", "handler-topic-test-cluster-secret")
	if err != nil {
		t.Fatalf("topic store: %v", err)
	}
	env := &topicEnv{db: db}
	provider := &fakePushProvider{name: "apns", fn: func(_ context.Context, msg push.PushMessage) error {
		env.sends = append(env.sends, msg.DeviceToken)
		return nil
	}}
	env.h, env.logs = newObservedHandlers(topicTestManager(store, provider))
	return env
}

// topicTestManager dispatches every namespace to provider; a nil provider is
// a namespace with no push configured.
func topicTestManager(store push.PushTopicStore, provider push.PushProvider) *push.Manager {
	factory := func(context.Context, push.Config) []push.PushProvider {
		if provider == nil {
			return nil
		}
		return []push.PushProvider{provider}
	}
	m := push.NewManager(&fakeStore{}, nil, push.Defaults{}, factory, zap.NewNop())
	m.SetTopicStore(store)
	return m
}

func newObservedHandlers(m *push.Manager) (*Handlers, *observer.ObservedLogs) {
	core, logs := observer.New(zap.DebugLevel)
	return NewHandlersWithManager(m, nil, &fakeStore{}, &logging.ColoredLogger{Logger: zap.New(core)}), logs
}

// asCaller authenticates the request as a wallet whose token also carries the
// app's account_id claim — everything a handler could bind a topic to.
func asCaller(r *http.Request) *http.Request {
	ctx := context.WithValue(r.Context(), ctxkeys.NamespaceOverride, topicNS)
	ctx = context.WithValue(ctx, ctxkeys.JWT, &authsvc.JWTClaims{
		Sub: topicWallet, Namespace: topicNS, Custom: map[string]string{"account_id": topicAccount},
	})
	return r.WithContext(ctx)
}

func topicRequest(t *testing.T, method, path string, body any) *http.Request {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return asCaller(httptest.NewRequest(method, path, bytes.NewReader(raw)))
}

func (e *topicEnv) register(t *testing.T, secret, token string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	e.h.RegisterTopicHandler(rr, topicRequest(t, http.MethodPost, "/v1/push/topics",
		RegisterTopicRequest{TopicSecret: secret, Provider: "apns", Token: token}))
	return rr
}

func (e *topicEnv) unregister(t *testing.T, secret string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	e.h.UnregisterTopicHandler(rr, topicRequest(t, http.MethodDelete, "/v1/push/topics",
		UnregisterTopicRequest{TopicSecret: secret}))
	return rr
}

// deliversTo reports which token a topic now delivers to, by sending to it
// through the admin send route. Empty when the topic is not registered.
func (e *topicEnv) deliversTo(t *testing.T, topicID string) string {
	t.Helper()
	before := len(e.sends)
	rr := httptest.NewRecorder()
	e.h.SendTopicHandler(rr, topicRequest(t, http.MethodPost, "/v1/push/topics/send",
		SendTopicRequest{TopicID: topicID, PushContent: PushContent{Title: "hi"}}))
	if rr.Code == http.StatusNotFound {
		return ""
	}
	if rr.Code != http.StatusOK || len(e.sends) != before+1 {
		t.Fatalf("send to %s: %d %s", topicID, rr.Code, rr.Body.String())
	}
	return e.sends[len(e.sends)-1]
}

func mustTopicID(t *testing.T, secret string) string {
	t.Helper()
	id, err := push.TopicIDFromSecret(secret)
	if err != nil {
		t.Fatalf("topic id: %v", err)
	}
	return id
}

func TestRegisterTopicHandler_happyPath(t *testing.T) {
	env := newTopicEnv(t)
	rr := env.register(t, secretA, tokenA)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var resp RegisterTopicResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.TopicID != mustTopicID(t, secretA) || resp.ExpiresAt == 0 {
		t.Errorf("response = %+v, want topic_id = sha256(secret) and an expiry", resp)
	}
	if got := env.deliversTo(t, resp.TopicID); got != tokenA {
		t.Errorf("topic delivers to %q, want %q", got, tokenA)
	}
}

// Neither the caller's subject nor its account claim nor the secret is stored
// anywhere in the row, and none of them — nor the topic id — is logged.
func TestRegisterTopicHandler_storesAndLogsNoCallerIdentity(t *testing.T) {
	env := newTopicEnv(t)
	if rr := env.register(t, secretA, tokenA); rr.Code != http.StatusOK {
		t.Fatalf("register: %d %s", rr.Code, rr.Body.String())
	}

	rows, err := env.db.Query(`SELECT * FROM push_topics`)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatalf("scan: %v", err)
		}
		for i, v := range vals {
			s := strings.ToLower(fmt.Sprint(v))
			for _, leaked := range []string{topicWallet, topicAccount, secretA, tokenA} {
				if strings.Contains(s, strings.ToLower(leaked)) {
					t.Errorf("column %s holds %q", cols[i], leaked)
				}
			}
		}
	}
	assertLogsFree(t, env.logs, topicWallet, topicAccount, secretA, mustTopicID(t, secretA))
}

func assertLogsFree(t *testing.T, logs *observer.ObservedLogs, values ...string) {
	t.Helper()
	for _, entry := range logs.All() {
		line := entry.Message + fmt.Sprint(entry.ContextMap())
		for _, v := range values {
			if strings.Contains(line, v) {
				t.Errorf("log %q carries %q", entry.Message, v)
			}
		}
	}
}

// failingTopicStore fails every call, to exercise the handlers' error logging.
type failingTopicStore struct{}

var errStoreDown = errors.New("rqlite: leader unavailable")

func (failingTopicStore) Register(context.Context, push.PushTopic) (int64, error) {
	return 0, errStoreDown
}
func (failingTopicStore) Unregister(context.Context, string, string) error { return errStoreDown }
func (failingTopicStore) Get(context.Context, string, string) (*push.PushTopic, error) {
	return nil, errStoreDown
}

func TestRegisterTopicHandler_storeFailureLogsNoCallerIdentity(t *testing.T) {
	m := push.NewManager(&fakeStore{}, nil, push.Defaults{}, nil, zap.NewNop())
	m.SetTopicStore(failingTopicStore{})
	h, logs := newObservedHandlers(m)

	rr := httptest.NewRecorder()
	h.RegisterTopicHandler(rr, topicRequest(t, http.MethodPost, "/v1/push/topics",
		RegisterTopicRequest{TopicSecret: secretA, Provider: "apns", Token: tokenA}))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", rr.Code)
	}
	rr = httptest.NewRecorder()
	h.UnregisterTopicHandler(rr, topicRequest(t, http.MethodDelete, "/v1/push/topics",
		UnregisterTopicRequest{TopicSecret: secretA}))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("unregister status %d, want 500", rr.Code)
	}
	if logs.Len() == 0 {
		t.Fatal("a store failure was not logged")
	}
	assertLogsFree(t, logs, topicWallet, topicAccount, secretA, mustTopicID(t, secretA))
}

func TestRegisterTopicHandler_refreshWithTheSameSecret(t *testing.T) {
	env := newTopicEnv(t)
	env.register(t, secretA, tokenA)
	if rr := env.register(t, secretA, tokenB); rr.Code != http.StatusOK {
		t.Fatalf("refresh: %d %s", rr.Code, rr.Body.String())
	}
	if got := env.deliversTo(t, mustTopicID(t, secretA)); got != tokenB {
		t.Errorf("after refresh the topic delivers to %q, want %q", got, tokenB)
	}
}

// Knowing a topic id is what a sender has. It must not be enough to re-point
// or remove the topic: the id presented as a secret is a different topic.
func TestTopicHandlers_topicIDAloneCannotRepointOrRemove(t *testing.T) {
	env := newTopicEnv(t)
	env.register(t, secretA, tokenA)
	idA := mustTopicID(t, secretA)

	if rr := env.unregister(t, idA); rr.Code != http.StatusNotFound {
		t.Errorf("unregister with the topic id as the secret: %d, want 404", rr.Code)
	}
	if rr := env.unregister(t, secretB); rr.Code != http.StatusNotFound {
		t.Errorf("unregister with another secret: %d, want 404", rr.Code)
	}
	if rr := env.register(t, idA, tokenB); rr.Code != http.StatusOK {
		t.Fatalf("register under the id-as-secret: %d", rr.Code)
	}
	if got := env.deliversTo(t, idA); got != tokenA {
		t.Errorf("topic A now delivers to %q; a sender re-pointed it", got)
	}
}

func TestUnregisterTopicHandler_happyPath(t *testing.T) {
	env := newTopicEnv(t)
	env.register(t, secretA, tokenA)
	if rr := env.unregister(t, secretA); rr.Code != http.StatusOK {
		t.Fatalf("unregister: %d %s", rr.Code, rr.Body.String())
	}
	if got := env.deliversTo(t, mustTopicID(t, secretA)); got != "" {
		t.Errorf("removed topic still delivers to %q", got)
	}
	if rr := env.unregister(t, secretA); rr.Code != http.StatusNotFound {
		t.Errorf("second unregister: %d, want 404", rr.Code)
	}
}

func TestRegisterTopicHandler_rejectsBadInput(t *testing.T) {
	env := newTopicEnv(t)
	cases := map[string]RegisterTopicRequest{
		"short secret":     {TopicSecret: "abcd", Provider: "apns", Token: tokenA},
		"non-hex secret":   {TopicSecret: strings.Repeat("zz", 16), Provider: "apns", Token: tokenA},
		"missing secret":   {Provider: "apns", Token: tokenA},
		"unknown provider": {TopicSecret: secretA, Provider: "carrier-pigeon", Token: tokenA},
		"missing token":    {TopicSecret: secretA, Provider: "apns"},
		"oversized token":  {TopicSecret: secretA, Provider: "apns", Token: strings.Repeat("t", MaxTokenBytes+1)},
	}
	for name, body := range cases {
		rr := httptest.NewRecorder()
		env.h.RegisterTopicHandler(rr, topicRequest(t, http.MethodPost, "/v1/push/topics", body))
		if rr.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", name, rr.Code)
		}
	}
	rr := httptest.NewRecorder()
	env.h.RegisterTopicHandler(rr, asCaller(httptest.NewRequest(http.MethodPost, "/v1/push/topics", strings.NewReader("{"))))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("malformed json: %d, want 400", rr.Code)
	}
	var n int
	if err := env.db.QueryRow(`SELECT COUNT(*) FROM push_topics`).Scan(&n); err != nil || n != 0 {
		t.Errorf("rows after only bad input = %d (%v)", n, err)
	}
}

func TestTopicHandlers_refuseWithoutNamespaceOrStore(t *testing.T) {
	env := newTopicEnv(t)
	raw, _ := json.Marshal(RegisterTopicRequest{TopicSecret: secretA, Provider: "apns", Token: tokenA})
	rr := httptest.NewRecorder()
	env.h.RegisterTopicHandler(rr, httptest.NewRequest(http.MethodPost, "/v1/push/topics", bytes.NewReader(raw)))
	if rr.Code != http.StatusForbidden {
		t.Errorf("no namespace: %d, want 403", rr.Code)
	}

	legacy := newHandlers(&fakeStore{}, nil) // no Manager, so no topic store
	for name, fn := range map[string]http.HandlerFunc{
		"register": legacy.RegisterTopicHandler, "unregister": legacy.UnregisterTopicHandler, "send": legacy.SendTopicHandler,
	} {
		rr := httptest.NewRecorder()
		fn(rr, topicRequest(t, http.MethodPost, "/v1/push/topics", RegisterTopicRequest{}))
		if rr.Code != http.StatusServiceUnavailable {
			t.Errorf("%s without a topic store: %d, want 503", name, rr.Code)
		}
	}
}

func TestSendTopicHandler_outcomes(t *testing.T) {
	env := newTopicEnv(t)
	env.register(t, secretA, tokenA)
	send := func(r *http.Request) int {
		rr := httptest.NewRecorder()
		env.h.SendTopicHandler(rr, r)
		return rr.Code
	}
	body := func(id string) SendTopicRequest {
		return SendTopicRequest{TopicID: id, PushContent: PushContent{Title: "hi"}}
	}

	if c := send(topicRequest(t, http.MethodPost, "/v1/push/topics/send", body(mustTopicID(t, secretA)))); c != http.StatusOK {
		t.Errorf("registered topic: %d, want 200", c)
	}
	if c := send(topicRequest(t, http.MethodPost, "/v1/push/topics/send", body(mustTopicID(t, secretB)))); c != http.StatusNotFound {
		t.Errorf("unknown topic: %d, want 404", c)
	}
	if c := send(topicRequest(t, http.MethodPost, "/v1/push/topics/send", body("not-a-topic-id"))); c != http.StatusBadRequest {
		t.Errorf("malformed topic id: %d, want 400", c)
	}
	raw, _ := json.Marshal(body(mustTopicID(t, secretA)))
	if c := send(httptest.NewRequest(http.MethodPost, "/v1/push/topics/send", bytes.NewReader(raw))); c != http.StatusForbidden {
		t.Errorf("no namespace: %d, want 403", c)
	}
	if len(env.sends) != 1 {
		t.Errorf("sends = %v, want exactly the one to the registered topic", env.sends)
	}
}

func TestSendTopicHandler_deliveryFailureIs502(t *testing.T) {
	env := newTopicEnv(t)
	env.register(t, secretA, tokenA)
	failing := &fakePushProvider{name: "apns", fn: func(context.Context, push.PushMessage) error {
		return &push.PushError{HTTPStatus: http.StatusGone, Reason: "Unregistered", Unregistered: true}
	}}
	h, _ := newObservedHandlers(topicTestManager(env.h.topicStore(), failing))

	rr := httptest.NewRecorder()
	h.SendTopicHandler(rr, topicRequest(t, http.MethodPost, "/v1/push/topics/send",
		SendTopicRequest{TopicID: mustTopicID(t, secretA), PushContent: PushContent{Title: "hi"}}))
	if rr.Code != http.StatusBadGateway {
		t.Errorf("provider 410: %d, want 502", rr.Code)
	}
}

func TestSendTopicHandler_pushNotConfiguredIs503(t *testing.T) {
	env := newTopicEnv(t)
	env.register(t, secretA, tokenA)
	h, _ := newObservedHandlers(topicTestManager(env.h.topicStore(), nil))

	rr := httptest.NewRecorder()
	h.SendTopicHandler(rr, topicRequest(t, http.MethodPost, "/v1/push/topics/send",
		SendTopicRequest{TopicID: mustTopicID(t, secretA), PushContent: PushContent{Title: "hi"}}))
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("namespace without a provider: %d, want 503", rr.Code)
	}
}
