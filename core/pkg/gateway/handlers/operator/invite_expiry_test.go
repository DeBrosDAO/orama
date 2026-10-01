package operator

import (
	"testing"
	"time"
)

func TestInviteRequestExpiry(t *testing.T) {
	for name, c := range map[string]struct {
		req  InviteRequest
		want time.Duration
	}{
		"nothing asked is the default hour": {InviteRequest{}, time.Hour},
		"seconds are honoured":              {InviteRequest{ExpirySeconds: 30}, 30 * time.Second},
		"seconds win over minutes":          {InviteRequest{ExpirySeconds: 30, ExpiryMinutes: 1}, 30 * time.Second},
		"minutes from an older CLI":         {InviteRequest{ExpiryMinutes: 5}, 5 * time.Minute},
		"capped at an hour":                 {InviteRequest{ExpirySeconds: 86400}, time.Hour},
		"overflowing seconds are capped":    {InviteRequest{ExpirySeconds: 1 << 62}, time.Hour},
		"overflowing minutes are capped":    {InviteRequest{ExpiryMinutes: 1 << 62}, time.Hour},
	} {
		if got := c.req.expiry(); got != c.want {
			t.Errorf("%s: %s, want %s", name, got, c.want)
		}
	}
}

func TestInviteRequestValid_refusesANegativeExpiry(t *testing.T) {
	for _, req := range []InviteRequest{{ExpirySeconds: -1}, {ExpiryMinutes: -1}} {
		if req.valid() == nil {
			t.Errorf("%+v was accepted", req)
		}
	}
	if err := (InviteRequest{ExpirySeconds: 30}).valid(); err != nil {
		t.Errorf("a positive expiry was refused: %v", err)
	}
}
