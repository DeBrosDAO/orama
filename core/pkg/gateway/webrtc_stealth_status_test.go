package gateway

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/namespace"
)

func TestStealthToggleOutcome(t *testing.T) {
	boom := errors.New("dns provider down")
	cases := []struct {
		name   string
		enable bool
		err    error
		want   int
	}{
		{"enable_ok", true, nil, http.StatusOK},
		{"disable_ok", false, nil, http.StatusOK},
		{"disable_already_off_is_idempotent", false, namespace.ErrWebRTCStealthNotEnabled, http.StatusOK},
		{"disable_already_off_wrapped", false, fmt.Errorf("x: %w", namespace.ErrWebRTCStealthNotEnabled), http.StatusOK},
		{"enable_twice_conflicts", true, namespace.ErrWebRTCStealthAlreadyEnabled, http.StatusConflict},
		{"enable_without_webrtc_conflicts", true, namespace.ErrWebRTCNotEnabled, http.StatusConflict},
		{"disable_without_webrtc_conflicts", false, namespace.ErrWebRTCNotEnabled, http.StatusConflict},
		{"no_cluster", false, namespace.ErrClusterNotFound, http.StatusNotFound},
		{"real_failure", false, boom, http.StatusInternalServerError},
		{"enable_not_enabled_error_is_conflict", true, namespace.ErrWebRTCStealthNotEnabled, http.StatusConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, msg := stealthToggleOutcome(tc.enable, tc.err)
			if got != tc.want {
				t.Errorf("status = %d (%q), want %d", got, msg, tc.want)
			}
			if msg == "" {
				t.Error("empty message")
			}
		})
	}
}

func TestStealthToggleOutcome_internalErrorDetailIsNotExposed(t *testing.T) {
	_, msg := stealthToggleOutcome(true, errors.New("dial tcp 10.0.0.7:53: secret-host refused"))
	if strings.Contains(msg, "10.0.0.7") || strings.Contains(msg, "secret-host") {
		t.Errorf("500 message leaks the error: %q", msg)
	}
}

// The gateway mirrors the namespace package's codes instead of importing it;
// the two must not drift.
func TestStealthCodes_matchTheNamespacePackage(t *testing.T) {
	for got, want := range map[string]string{
		codeClusterNotFound:             namespace.CodeClusterNotFound,
		codeWebRTCNotEnabled:            namespace.CodeWebRTCNotEnabled,
		codeWebRTCStealthAlreadyEnabled: namespace.CodeWebRTCStealthAlreadyEnabled,
		codeWebRTCStealthNotEnabled:     namespace.CodeWebRTCStealthNotEnabled,
	} {
		if got != want {
			t.Errorf("gateway code %q, namespace code %q", got, want)
		}
	}
}
