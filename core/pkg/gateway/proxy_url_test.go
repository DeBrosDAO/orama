package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProxyTargetURL(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"plain", "/v1/storage/get/bafy", "http://10.0.0.3:10024/v1/storage/get/bafy"},
		{"query kept", "/v1/cache/get?key=a%20b&x=1", "http://10.0.0.3:10024/v1/cache/get?key=a%20b&x=1"},
		{"NUL stays escaped", "/v1/storage/get/%00", "http://10.0.0.3:10024/v1/storage/get/%00"},
		{"encoded question mark is not a query", "/v1/storage/get/a%3Fb", "http://10.0.0.3:10024/v1/storage/get/a%3Fb"},
		{"encoded slash kept", "/v1/storage/get/a%2Fb", "http://10.0.0.3:10024/v1/storage/get/a%2Fb"},
		{"space escaped", "/v1/storage/get/Qm%20x", "http://10.0.0.3:10024/v1/storage/get/Qm%20x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, tc.in, nil)
			got := proxyTargetURL("http://10.0.0.3:10024", r.URL)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
			if _, err := http.NewRequest(http.MethodGet, got, nil); err != nil {
				t.Errorf("http.NewRequest refuses %q: %v", got, err)
			}
		})
	}
}
