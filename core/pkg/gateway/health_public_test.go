package gateway

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/logging"
)

// /health is unauthenticated. The operator report keeps the error; the public
// report keeps the status. A failed check used to put the raw error — DSNs
// with credentials, internal addresses, library internals — on /health.
func TestPublicHealth_stripsCheckErrors(t *testing.T) {
	logger, _ := logging.NewColoredLogger(logging.ComponentGateway, false)
	g := &Gateway{logger: logger}
	secret := errors.New(`dial http://orama:hunter2@10.0.0.7:10100/db/query: connection refused`)

	full := g.failedCheck("rqlite", time.Now(), secret)
	if full.Status != "error" {
		t.Errorf("Status = %q, want error", full.Status)
	}
	if !strings.Contains(full.Error, "hunter2") {
		t.Errorf("the operator report dropped the error: %q", full.Error)
	}
	pub := publicHealth(map[string]any{
		"status": "unhealthy",
		"checks": map[string]checkResult{"rqlite": full},
	})
	encoded := fmt.Sprint(pub)
	for _, leak := range []string{"hunter2", "10.0.0.7", "10100", "dial"} {
		if strings.Contains(encoded, leak) {
			t.Errorf("public health leaks %q: %s", leak, encoded)
		}
	}
	checks, _ := pub["checks"].(map[string]map[string]string)
	if checks["rqlite"]["status"] != "error" {
		t.Errorf("public check = %#v", checks["rqlite"])
	}
}
