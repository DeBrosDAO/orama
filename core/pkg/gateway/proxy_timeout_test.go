package gateway

import (
	"context"
	"errors"
	"net/url"
	"testing"
)

// Bug #219: distinguish proxy timeout from generic connection failure
// so the canonical envelope carries the right error code.

func TestIsLongRunningProxyPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		// Long-running known classes.
		{"/v1/storage/upload", true},
		{"/v1/storage/upload/foo", true},
		{"/v1/storage/pin", true},
		{"/v1/invoke/myns/myfn", true},
		{"/v1/functions/abc/invoke", true},
		{"/v1/functions/abc/ws", true},

		// Fast paths — must default to the 30s budget.
		{"/v1/health", false},
		{"/v1/auth/verify", false},
		{"/v1/pubsub/publish", false}, // single-publish, fast
		{"/v1/functions/abc/logs", false},
		{"/v1/db/query", false},
		{"", false},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			if got := isLongRunningProxyPath(c.path); got != c.want {
				t.Errorf("isLongRunningProxyPath(%q) = %v, want %v", c.path, got, c.want)
			}
		})
	}
}

func TestIsProxyTimeoutErr(t *testing.T) {
	t.Run("nil_is_not_timeout", func(t *testing.T) {
		if isProxyTimeoutErr(nil) {
			t.Error("nil error must not register as timeout")
		}
	})

	t.Run("context_deadline_exceeded_is_timeout", func(t *testing.T) {
		if !isProxyTimeoutErr(context.DeadlineExceeded) {
			t.Error("context.DeadlineExceeded must register as timeout")
		}
	})

	t.Run("wrapped_context_deadline_is_timeout", func(t *testing.T) {
		err := &url.Error{Op: "Get", URL: "http://x", Err: context.DeadlineExceeded}
		if !isProxyTimeoutErr(err) {
			t.Error("url.Error wrapping context.DeadlineExceeded must register as timeout")
		}
	})

	t.Run("connection_refused_is_NOT_timeout", func(t *testing.T) {
		// Genuine connection failures should map to SERVICE_UNAVAILABLE, not TIMEOUT.
		err := errors.New("connection refused")
		if isProxyTimeoutErr(err) {
			t.Error("plain connection error must not register as timeout")
		}
	})

	t.Run("unrelated_error_is_NOT_timeout", func(t *testing.T) {
		err := errors.New("dns lookup failed")
		if isProxyTimeoutErr(err) {
			t.Error("unrelated errors must not register as timeout")
		}
	})
}
