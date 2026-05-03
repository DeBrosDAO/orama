package push

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"go.uber.org/zap"
)

// fakeProvider records every Send call.
type fakeProvider struct {
	name      string
	sent      int32
	lastToken string
	err       error
}

func (f *fakeProvider) Name() string { return f.name }
func (f *fakeProvider) Send(ctx context.Context, msg PushMessage) error {
	atomic.AddInt32(&f.sent, 1)
	f.lastToken = msg.DeviceToken
	return f.err
}

// fakeStore is an in-memory PushDeviceStore.
type fakeStore struct {
	devices []PushDevice
	err     error
}

func (s *fakeStore) Upsert(ctx context.Context, dev PushDevice) error {
	if s.err != nil {
		return s.err
	}
	s.devices = append(s.devices, dev)
	return nil
}
func (s *fakeStore) Delete(ctx context.Context, ns, id string) error { return nil }
func (s *fakeStore) ListForUser(ctx context.Context, ns, userID string) ([]PushDevice, error) {
	if s.err != nil {
		return nil, s.err
	}
	out := []PushDevice{}
	for _, d := range s.devices {
		if d.Namespace == ns && d.UserID == userID {
			out = append(out, d)
		}
	}
	return out, nil
}

func TestSendToUser_no_devices_returns_nil(t *testing.T) {
	d := New(&fakeStore{}, zap.NewNop())
	if err := d.SendToUser(context.Background(), "ns", "u", PushMessage{Title: "x"}); err != nil {
		t.Fatalf("expected nil for no devices, got: %v", err)
	}
}

func TestSendToUser_routes_to_correct_provider(t *testing.T) {
	store := &fakeStore{devices: []PushDevice{
		{Namespace: "ns", UserID: "u", Provider: "ntfy", Token: "ntfy-tok"},
		{Namespace: "ns", UserID: "u", Provider: "expo", Token: "expo-tok"},
	}}
	ntfy := &fakeProvider{name: "ntfy"}
	expo := &fakeProvider{name: "expo"}

	d := New(store, zap.NewNop())
	d.Register(ntfy)
	d.Register(expo)

	if err := d.SendToUser(context.Background(), "ns", "u", PushMessage{Title: "hi"}); err != nil {
		t.Fatalf("SendToUser: %v", err)
	}

	if atomic.LoadInt32(&ntfy.sent) != 1 || ntfy.lastToken != "ntfy-tok" {
		t.Errorf("ntfy provider not called correctly: sent=%d token=%s", ntfy.sent, ntfy.lastToken)
	}
	if atomic.LoadInt32(&expo.sent) != 1 || expo.lastToken != "expo-tok" {
		t.Errorf("expo provider not called correctly: sent=%d token=%s", expo.sent, expo.lastToken)
	}
}

func TestSendToUser_unknown_provider_returns_error_continues(t *testing.T) {
	store := &fakeStore{devices: []PushDevice{
		{Namespace: "ns", UserID: "u", Provider: "ghost", Token: "tok"},
		{Namespace: "ns", UserID: "u", Provider: "ntfy", Token: "real"},
	}}
	ntfy := &fakeProvider{name: "ntfy"}

	d := New(store, zap.NewNop())
	d.Register(ntfy)

	err := d.SendToUser(context.Background(), "ns", "u", PushMessage{})
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
	if !errors.Is(err, ErrUnknownProvider) {
		t.Errorf("expected ErrUnknownProvider, got %v", err)
	}
	// ntfy should still have been called.
	if atomic.LoadInt32(&ntfy.sent) != 1 {
		t.Error("ntfy should have been called for the second device")
	}
}

func TestSendToUser_provider_failure_returned_but_other_devices_still_processed(t *testing.T) {
	store := &fakeStore{devices: []PushDevice{
		{Namespace: "ns", UserID: "u", Provider: "expo", Token: "tok-1"},
		{Namespace: "ns", UserID: "u", Provider: "ntfy", Token: "tok-2"},
	}}
	expoErr := errors.New("expo down")
	expo := &fakeProvider{name: "expo", err: expoErr}
	ntfy := &fakeProvider{name: "ntfy"}

	d := New(store, zap.NewNop())
	d.Register(expo)
	d.Register(ntfy)

	err := d.SendToUser(context.Background(), "ns", "u", PushMessage{})
	if !errors.Is(err, expoErr) {
		t.Errorf("expected expo error, got %v", err)
	}
	if atomic.LoadInt32(&ntfy.sent) != 1 {
		t.Error("ntfy should have been called even though expo failed")
	}
}

func TestSendToUser_store_error_propagated(t *testing.T) {
	storeErr := errors.New("store boom")
	d := New(&fakeStore{err: storeErr}, zap.NewNop())

	err := d.SendToUser(context.Background(), "ns", "u", PushMessage{})
	if err == nil || !errors.Is(err, storeErr) {
		t.Errorf("expected store error, got %v", err)
	}
}

func TestRegister_replaces_existing_provider(t *testing.T) {
	d := New(&fakeStore{}, zap.NewNop())
	a := &fakeProvider{name: "ntfy"}
	b := &fakeProvider{name: "ntfy"}
	d.Register(a)
	d.Register(b)
	if d.Provider("ntfy") != b {
		t.Error("expected second Register to replace the first")
	}
}
