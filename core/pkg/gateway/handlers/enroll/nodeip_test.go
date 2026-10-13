package enroll

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
)

func TestReservedNodeIP(t *testing.T) {
	tests := []struct {
		ip   string
		want bool
	}{
		{"203.0.113.10", false},
		{"8.8.8.8", false},
		{"127.0.0.1", true},
		{"10.0.0.4", true},
		{"192.168.1.1", true},
		{"172.16.0.1", true},
		{"169.254.169.254", true},
		{"100.64.0.1", true},
		{"0.0.0.0", true},
		{"224.0.0.1", true},
	}
	for _, tc := range tests {
		got := reservedNodeIP(net.ParseIP(tc.ip))
		if got != tc.want {
			t.Errorf("reservedNodeIP(%s) = %v, want %v", tc.ip, got, tc.want)
		}
	}
	if !reservedNodeIP(nil) {
		t.Error("reservedNodeIP(nil) = false, want true")
	}
}

// A reserved address must be refused before the invite token is consumed, so
// pointing enroll at loopback or a cloud metadata address cannot spend a token
// and cannot make this gateway issue an outbound request.
func TestHandleEnroll_refusesAReservedNodeIP(t *testing.T) {
	h := NewHandler(zap.NewNop(), nil, "")
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.64.1.1"} {
		body, err := json.Marshal(EnrollRequest{Code: "abcd", Token: "tok", NodeIP: ip})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/v1/node/enroll", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		h.HandleEnroll(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400 — this address is an SSRF and a useless WG endpoint",
				ip, rec.Code)
		}
	}
}
