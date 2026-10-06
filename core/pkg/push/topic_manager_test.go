package push

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"go.uber.org/zap"
)

// newTopicTestManager returns a Manager whose every namespace dispatches to
// provider, with a real sqlite-backed topic store.
func newTopicTestManager(t *testing.T, provider *fakeProvider) (*Manager, *RqliteTopicStore, *time.Time) {
	t.Helper()
	store, _, now := newTopicTestStore(t)
	factory := func(context.Context, Config) []PushProvider { return []PushProvider{provider} }
	m := NewManager(&fakeDeviceStore{}, newFakeConfigStore(), Defaults{NtfyBaseURL: "http://ntfy"}, factory, zap.NewNop())
	m.SetTopicStore(store)
	return m, store, now
}

func TestManager_SendToTopicDetailed_deliversToTheTopicsDevice(t *testing.T) {
	provider := &fakeProvider{name: "apns"}
	m, store, _ := newTopicTestManager(t, provider)
	id := topicIDFor(t, 1)
	mustRegisterTopic(t, store, topicTestNS, id, topicTestToken)

	res, err := m.SendToTopicDetailed(context.Background(), topicTestNS, id, PushMessage{Title: "hi"})
	if err != nil {
		t.Fatalf("SendToTopicDetailed: %v", err)
	}
	if !res.Ok || res.DevicesAttempted != 1 || res.DevicesSucceeded != 1 {
		t.Errorf("result = %+v, want one successful delivery", res)
	}
	if provider.lastToken != topicTestToken {
		t.Errorf("provider got token %q, want the topic's token", provider.lastToken)
	}
	if res.Results[0].HTTPStatus != http.StatusOK {
		t.Errorf("http status = %d, want 200", res.Results[0].HTTPStatus)
	}
}

// A topic registered in one namespace is not reachable from another.
func TestManager_SendToTopicDetailed_isNamespaceScoped(t *testing.T) {
	provider := &fakeProvider{name: "apns"}
	m, store, _ := newTopicTestManager(t, provider)
	id := topicIDFor(t, 1)
	mustRegisterTopic(t, store, "tenant-a", id, topicTestToken)

	_, err := m.SendToTopicDetailed(context.Background(), "tenant-b", id, PushMessage{Title: "hi"})
	if !errors.Is(err, ErrTopicNotFound) {
		t.Fatalf("err = %v, want ErrTopicNotFound", err)
	}
	if provider.sent != 0 {
		t.Errorf("provider called %d times across a namespace boundary", provider.sent)
	}
}

func TestManager_SendToTopicDetailed_expiredTopicIsNotDelivered(t *testing.T) {
	provider := &fakeProvider{name: "apns"}
	m, store, now := newTopicTestManager(t, provider)
	id := topicIDFor(t, 1)
	exp := mustRegisterTopic(t, store, topicTestNS, id, topicTestToken)
	*now = time.Unix(exp, 0)

	if _, err := m.SendToTopicDetailed(context.Background(), topicTestNS, id, PushMessage{Title: "hi"}); !errors.Is(err, ErrTopicNotFound) {
		t.Fatalf("err = %v, want ErrTopicNotFound", err)
	}
	if provider.sent != 0 {
		t.Error("delivered to an expired topic")
	}
}

func TestManager_SendToTopicDetailed_reportsProviderFailure(t *testing.T) {
	provider := &fakeProvider{name: "apns", err: &PushError{HTTPStatus: 410, Reason: "Unregistered", Unregistered: true}}
	m, store, _ := newTopicTestManager(t, provider)
	id := topicIDFor(t, 1)
	mustRegisterTopic(t, store, topicTestNS, id, topicTestToken)

	res, err := m.SendToTopicDetailed(context.Background(), topicTestNS, id, PushMessage{Title: "hi"})
	if err != nil {
		t.Fatalf("a per-device failure became a Go error: %v", err)
	}
	if res.Ok || !res.Results[0].Unregistered || res.Results[0].HTTPStatus != 410 {
		t.Errorf("result = %+v, want the provider's 410 Unregistered", res)
	}
}

func TestManager_SendToTopicDetailed_withoutTopicStore(t *testing.T) {
	m := NewManager(&fakeDeviceStore{}, newFakeConfigStore(), Defaults{}, nil, zap.NewNop())
	if _, err := m.SendToTopicDetailed(context.Background(), topicTestNS, topicIDFor(t, 1), PushMessage{}); !errors.Is(err, ErrTopicsNotConfigured) {
		t.Errorf("err = %v, want ErrTopicsNotConfigured", err)
	}
}

func TestManager_SendToTopicDetailed_pushNotConfigured(t *testing.T) {
	store, _, _ := newTopicTestStore(t)
	empty := func(context.Context, Config) []PushProvider { return nil }
	m := NewManager(&fakeDeviceStore{}, newFakeConfigStore(), Defaults{}, empty, zap.NewNop())
	m.SetTopicStore(store)
	if _, err := m.SendToTopicDetailed(context.Background(), topicTestNS, topicIDFor(t, 1), PushMessage{}); !errors.Is(err, ErrPushNotConfigured) {
		t.Errorf("err = %v, want ErrPushNotConfigured", err)
	}
}
