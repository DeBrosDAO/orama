package gateway

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
)

// bugboard #2508: the namespace auth check logged a caller's raw API key.
func TestLoggableOwnerID_apiKeyIsNeverLogged(t *testing.T) {
	const key = "ak_apcE-vlJ6VAGcAbGXAMgcNpp:anchat-test"
	got := loggableOwnerID("api_key", key)
	for _, part := range []string{key, "ak_apcE", "vlJ6VAGcAb", "anchat-test"} {
		if strings.Contains(got, part) {
			t.Fatalf("logged owner %q contains %q from the key", got, part)
		}
	}
	if got != auth.KeyFingerprint(key) {
		t.Errorf("logged owner = %q, want the key's fingerprint %q", got, auth.KeyFingerprint(key))
	}
	if loggableOwnerID("api_key", key) != got {
		t.Error("the fingerprint is not stable, so log lines for one key cannot be correlated")
	}
	if loggableOwnerID("api_key", key+"x") == got {
		t.Error("two keys share a fingerprint")
	}
}

func TestLoggableOwnerID_walletIsLoggedAsIs(t *testing.T) {
	const wallet = "0xb5d8a496c8b2412990d7D467E17727fdF5954afC"
	if got := loggableOwnerID("wallet", wallet); got != wallet {
		t.Errorf("loggableOwnerID(wallet) = %q, want the address", got)
	}
}
