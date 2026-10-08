package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/handlers/operator"
	"github.com/DeBrosOfficial/network/pkg/logging"
)

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v: %s", err, rec.Body.String())
	}
	return body
}

// A wallet at its cap is 403 NAMESPACE_QUOTA, the code a create at the cap
// answers, carrying the limit and a hint.
func TestRefuseTransfer_aWalletAtItsCapIsNamespaceQuota(t *testing.T) {
	g := &Gateway{}
	rec := httptest.NewRecorder()

	g.refuseTransfer(rec, fmt.Errorf("transfer: %w", &auth.ErrNamespaceQuota{Wallet: "0xfull", Cap: 10}))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	body := decodeBody(t, rec)
	if body["code"] != "NAMESPACE_QUOTA" {
		t.Errorf("code = %v, want NAMESPACE_QUOTA", body["code"])
	}
	if body["limit"] != float64(10) || body["wallet"] != "0xfull" {
		t.Errorf("the refusal does not name the wallet and its limit: %v", body)
	}
	if h, _ := body["hint"].(string); h == "" {
		t.Error("the refusal has no hint")
	}
}

func TestRefuseTransfer_anyOtherRefusalStaysABadRequest(t *testing.T) {
	g := &Gateway{}
	rec := httptest.NewRecorder()

	g.refuseTransfer(rec, errors.New("namespace \"acme\" already belongs to 0xnext"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// A cap that cannot be read is not a licence to transfer: the answer is a
// retryable 503 and, for a setting this binary cannot enforce, says so.
func TestRefuseUnreadableCap_isRetryableAndSaysWhy(t *testing.T) {
	logger, err := logging.NewColoredLogger(logging.ComponentGateway, false)
	if err != nil {
		t.Fatal(err)
	}
	g := &Gateway{logger: logger}

	for name, cause := range map[string]error{
		"registry down":     errors.New("connection refused"),
		"setting is broken": &operator.PolicyConfigError{Reason: "max_namespaces_per_wallet is \"x\""},
	} {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			g.refuseUnreadableCap(rec, fmt.Errorf("read the cap: %w", cause))
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503", rec.Code)
			}
		})
	}
}

func TestWalletNamespaceCap_noRegistryIsAnError(t *testing.T) {
	g := &Gateway{}
	if _, err := g.walletNamespaceCap(context.Background()); err == nil {
		t.Fatal("a gateway with no registry reported a cap")
	}
}
