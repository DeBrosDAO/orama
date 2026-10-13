package gateway

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	webrtchandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/webrtc"
	"github.com/DeBrosOfficial/network/pkg/logging"
)

const testStealthHost = "cdn-24c2fc1cfeab.stagenet.example.net"

func turnURIsFor(t *testing.T, cfg *Config) []string {
	t.Helper()
	logger, err := logging.NewColoredLogger(logging.ComponentGeneral, false)
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	h := webrtchandlers.NewWebRTCHandlers(logger, "", 30000, cfg.TURNDomain, cfg.TURNSecret, nil)
	applyTURNHosts(h, cfg)

	req := httptest.NewRequest("POST", "/v1/webrtc/turn/credentials", nil)
	req = req.WithContext(context.WithValue(req.Context(), ctxkeys.NamespaceOverride, "ns-a"))
	w := httptest.NewRecorder()
	h.CredentialsHandler(w, req)
	if w.Code != 200 {
		t.Fatalf("credentials status = %d: %s", w.Code, w.Body.String())
	}
	var out struct {
		URIs []string `json:"uris"`
	}
	if err := json.NewDecoder(w.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out.URIs
}

// A gateway whose config names a stealth host advertises turns:<host>:443 as
// the last rung. Before applyTURNHosts nothing handed Config.StealthCDNDomain
// to the credentials handler, so enabling stealth never changed the ladder.
func TestApplyTURNHosts_stealth_host_is_advertised(t *testing.T) {
	uris := turnURIsFor(t, &Config{
		TURNDomain:       "turn.ns-ns-a.stagenet.example.net",
		TURNSecret:       "secret",
		StealthCDNDomain: testStealthHost,
	})
	if len(uris) != 4 {
		t.Fatalf("want the 3 baseline rungs plus the stealth rung, got %v", uris)
	}
	if want := "turns:" + testStealthHost + ":443"; uris[3] != want {
		t.Errorf("last rung = %q, want %q", uris[3], want)
	}
}

func TestApplyTURNHosts_no_stealth_keeps_baseline_ladder(t *testing.T) {
	uris := turnURIsFor(t, &Config{
		TURNDomain: "turn.ns-ns-a.stagenet.example.net",
		TURNSecret: "secret",
	})
	if len(uris) != 3 {
		t.Fatalf("want only the 3 baseline rungs, got %v", uris)
	}
	for _, u := range uris {
		if strings.HasSuffix(u, ":443") {
			t.Errorf("stealth rung advertised without stealth: %s", u)
		}
	}
}

func TestApplyTURNHosts_turns_uses_single_label_host(t *testing.T) {
	uris := turnURIsFor(t, &Config{
		TURNDomain: "turn.ns-ns-a.stagenet.example.net",
		TURNSecret: "secret",
	})
	if want := "turns:turn-ns-a.stagenet.example.net:5349"; uris[2] != want {
		t.Errorf("TURNS rung = %q, want %q", uris[2], want)
	}
}
