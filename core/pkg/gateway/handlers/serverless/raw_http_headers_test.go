package serverless

import "testing"

// Bugboard #835 hardening (flagged by code + security review): a raw-HTTP
// tenant function must not be able to set/overwrite gateway-owned trace/auth
// headers or hop-by-hop framing headers.

func TestIsReservedResponseHeader(t *testing.T) {
	reserved := []string{
		"X-Request-ID", "x-request-id", "X-Duration-Ms",
		"Content-Length", "Transfer-Encoding", "Connection", "Keep-Alive",
		"Proxy-Authenticate", "Proxy-Authorization", "TE", "Trailer", "Upgrade",
		"X-Internal-Auth", "x-internal-anything", "  X-Request-Id  ",
	}
	for _, h := range reserved {
		if !isReservedResponseHeader(h) {
			t.Errorf("isReservedResponseHeader(%q) = false; want true (must be protected)", h)
		}
	}

	allowed := []string{
		"Content-Type", "Cache-Control", "X-Custom", "ETag",
		"Access-Control-Allow-Origin", "Location", "Retry-After",
	}
	for _, h := range allowed {
		if isReservedResponseHeader(h) {
			t.Errorf("isReservedResponseHeader(%q) = true; want false (tenant may set it)", h)
		}
	}
}
