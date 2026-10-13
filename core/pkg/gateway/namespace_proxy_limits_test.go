package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	backuphandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/backup"
)

func TestRefuseOversizedProxyBody(t *testing.T) {
	limit := int64(backuphandlers.MaxRestoreBytes)
	cases := []struct {
		name   string
		path   string
		length int64
		want   bool
	}{
		{"restore over the limit", "/v1/namespace/restore", limit + 1, true},
		{"restore at the limit", "/v1/namespace/restore", limit, false},
		{"restore of unknown length", "/v1/namespace/restore", -1, false},
		{"restore with no body", "/v1/namespace/restore", 0, false},
		{"another path has no proxy limit", "/v1/storage/upload", limit * 4, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, c.path, strings.NewReader(""))
			r.ContentLength = c.length
			w := httptest.NewRecorder()
			if got := refuseOversizedProxyBody(w, r); got != c.want {
				t.Fatalf("refused = %v, want %v", got, c.want)
			}
			if c.want && w.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("status %d, want 413", w.Code)
			}
			if !c.want && w.Code != http.StatusOK {
				t.Fatalf("an accepted request was answered %d", w.Code)
			}
		})
	}
}
