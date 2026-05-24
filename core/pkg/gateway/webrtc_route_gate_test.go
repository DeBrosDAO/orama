package gateway

import (
	"testing"
)

// Bugboard #411 — WebRTC route registration gate.
//
// Pre-fix the gate was `cfg.WebRTCEnabled && cfg.SFUPort > 0`. The
// boolean flag was a silent-404 footgun: spawn-handler-provisioned
// namespace gateways defaulted to WebRTCEnabled=false even when their
// SFU service was running and SFUPort was set, so every call to
// /v1/webrtc/turn/credentials returned 404 (not 503, not 401) for
// months — AnChat hit this on devnet for ~3 months before reporting.
//
// Post-fix: SFUPort > 0 alone gates registration. The legacy
// WebRTCEnabled boolean is retained on the Config struct for spawn-
// request back-compat but ignored at the gate.
//
// These tests pin the new gate semantics so a future refactor of
// gateway.go's startup wiring can't silently re-introduce the
// AND-with-boolean misconfig class.

// All four tests below call the SAME `shouldRegisterWebRTCRoutes`
// helper that the runtime calls — defined alongside the gateway code
// in gateway.go. If the runtime gate changes, the test breaks
// immediately rather than silently passing while live behavior
// diverges (the classic "test duplicates implementation" anti-pattern).

func TestWebRTCRouteGate_RegistersWhenSFUPortSet_RegardlessOfWebRTCEnabled(t *testing.T) {
	// The actual #411 bug: WebRTCEnabled=false (default for spawn-
	// provisioned namespace gateways) + SFUPort>0 (operator did
	// configure the SFU). Pre-fix this returned `false` → no routes
	// → 404. Post-fix MUST return true.
	cfg := &Config{
		WebRTCEnabled: false,
		SFUPort:       7800,
		TURNSecret:    "shared-secret",
		TURNDomain:    "turn.example.com",
	}
	if !shouldRegisterWebRTCRoutes(cfg) {
		t.Errorf("BUG #411 REGRESSION: SFUPort=%d configured but routes not registered "+
			"because legacy WebRTCEnabled=false. This is exactly the silent-404 footgun "+
			"the fix was supposed to eliminate.", cfg.SFUPort)
	}
}

func TestWebRTCRouteGate_RegistersWhenBothEnabledAndPortSet(t *testing.T) {
	// Pre-fix happy path — operator explicitly opted in via the
	// legacy boolean. Must still register so existing configs work.
	cfg := &Config{
		WebRTCEnabled: true,
		SFUPort:       7800,
		TURNSecret:    "shared-secret",
	}
	if !shouldRegisterWebRTCRoutes(cfg) {
		t.Error("explicit WebRTCEnabled=true + SFUPort>0: routes MUST register (back-compat)")
	}
}

func TestWebRTCRouteGate_SkipsWhenSFUPortZero(t *testing.T) {
	// No SFU port = no functional SFU proxy = registering routes
	// would just produce broken 500s on /v1/webrtc/signal. Better to
	// not register. This is the "namespace genuinely doesn't want
	// WebRTC" path.
	cases := []struct {
		name string
		cfg  *Config
	}{
		{"both unset", &Config{}},
		{"webrtc explicitly enabled but no port", &Config{WebRTCEnabled: true, SFUPort: 0}},
		{"port is negative (sentinel)", &Config{SFUPort: -1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if shouldRegisterWebRTCRoutes(tc.cfg) {
				t.Errorf("SFUPort=%d: routes MUST NOT register without a real SFU port",
					tc.cfg.SFUPort)
			}
		})
	}
}

func TestWebRTCRouteGate_TURNSecretMissingStillRegisters(t *testing.T) {
	// Important: SFUPort>0 + TURNSecret="" should still REGISTER the
	// routes. /v1/webrtc/signal and /v1/webrtc/rooms work without TURN
	// (TURN is only for the credentials endpoint). And the credentials
	// handler internally returns 503 "TURN not configured" when secret
	// is missing — which is an ACTIONABLE error operators can fix,
	// unlike the silent 404 that #411 reported.
	//
	// If a future refactor moves the TURNSecret check into the gate,
	// /v1/webrtc/signal disappears too and SFU-only namespaces break.
	cfg := &Config{
		SFUPort:    7800,
		TURNSecret: "", // intentionally missing
	}
	if !shouldRegisterWebRTCRoutes(cfg) {
		t.Error("SFUPort>0 + TURNSecret empty: routes MUST still register so /v1/webrtc/signal works; " +
			"the credentials endpoint surfaces 503 internally for the missing secret")
	}
}
