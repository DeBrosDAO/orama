package namespace

import (
	"context"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/turn"
)

// Bugboard #158 (warm TURN secret reconcile) — turnSecretDrift decides whether
// a running TURN server's on-disk auth_secret still matches the current
// (DB-sourced) secret. A drift triggers a rewrite+restart so TURN stops
// validating with a stale secret the gateway no longer mints with.

func TestTurnSecretDrift(t *testing.T) {
	if turnSecretDrift("same", "same") {
		t.Error("identical secrets must NOT be reported as drift (would cause a needless restart)")
	}
	if !turnSecretDrift("stale-secret-from-a-previous-enable", "the-current-db-secret") {
		t.Error("a drifted auth_secret must be detected (would otherwise break all call auth)")
	}
	if !turnSecretDrift("", "the-current-db-secret") {
		t.Error("empty on-disk secret vs a real DB secret is drift")
	}
}

// ReconcileTURN must be a NO-OP (no error, no restart) when there is no TURN
// config on this node — os.ReadFile fails and it returns nil before touching
// systemd. With a zero SystemdSpawner (nil systemdMgr) a nil return proves no
// restart was attempted.
func TestReconcileTURN_noopWhenNoConfig(t *testing.T) {
	s := &SystemdSpawner{}
	for _, secret := range []string{"", "enc:WyRmCA/Aciphertext", "a-real-secret"} {
		if err := s.ReconcileTURN(context.Background(), "ns", "node", secret); err != nil {
			t.Errorf("ReconcileTURN must no-op (nil) when no TURN config exists (secret %q), got %v", secret, err)
		}
	}
}

// reconcileTURNConfigFields is the pure reconcile decision. These lock the exact
// drift rules the warm reconcile depends on.
func TestReconcileTURNConfigFields(t *testing.T) {
	const wcCert = "/caddy/wildcard_.base.crt"
	const wcKey = "/caddy/wildcard_.base.key"
	const selfSigned = "/ns/configs/turn-cert.pem"

	t.Run("secret drift only", func(t *testing.T) {
		cfg := &turn.Config{AuthSecret: "stale", TURNSListenAddr: "0.0.0.0:5349", TLSCertPath: wcCert}
		got := reconcileTURNConfigFields(cfg, "current", wcCert, wcKey)
		if len(got) != 1 || got[0] != "auth_secret" {
			t.Fatalf("want [auth_secret], got %v", got)
		}
		if cfg.AuthSecret != "current" {
			t.Errorf("secret not patched: %q", cfg.AuthSecret)
		}
	})

	t.Run("cert drift only — self-signed to wildcard", func(t *testing.T) {
		cfg := &turn.Config{AuthSecret: "current", TURNSListenAddr: "0.0.0.0:5349", TLSCertPath: selfSigned}
		got := reconcileTURNConfigFields(cfg, "current", wcCert, wcKey)
		if len(got) != 1 || got[0] != "tls_cert" {
			t.Fatalf("want [tls_cert], got %v", got)
		}
		if cfg.TLSCertPath != wcCert || cfg.TLSKeyPath != wcKey {
			t.Errorf("cert not switched to wildcard: %q / %q", cfg.TLSCertPath, cfg.TLSKeyPath)
		}
	})

	t.Run("both drift", func(t *testing.T) {
		cfg := &turn.Config{AuthSecret: "stale", TURNSListenAddr: "0.0.0.0:5349", TLSCertPath: selfSigned}
		got := reconcileTURNConfigFields(cfg, "current", wcCert, wcKey)
		if len(got) != 2 {
			t.Fatalf("want both fields reconciled, got %v", got)
		}
	})

	t.Run("no drift — in sync", func(t *testing.T) {
		cfg := &turn.Config{AuthSecret: "current", TURNSListenAddr: "0.0.0.0:5349", TLSCertPath: wcCert}
		if got := reconcileTURNConfigFields(cfg, "current", wcCert, wcKey); len(got) != 0 {
			t.Errorf("in-sync config must not reconcile (needless restart), got %v", got)
		}
	})

	t.Run("never writes an encrypted/empty secret", func(t *testing.T) {
		for _, bad := range []string{"", "enc:ciphertext"} {
			cfg := &turn.Config{AuthSecret: "keep-me", TURNSListenAddr: "0.0.0.0:5349", TLSCertPath: wcCert}
			if got := reconcileTURNConfigFields(cfg, bad, wcCert, wcKey); len(got) != 0 {
				t.Errorf("secret %q must not be written, got %v", bad, got)
			}
			if cfg.AuthSecret != "keep-me" {
				t.Errorf("secret overwritten with %q", cfg.AuthSecret)
			}
		}
	})

	t.Run("no wildcard available — cert left untouched", func(t *testing.T) {
		cfg := &turn.Config{AuthSecret: "current", TURNSListenAddr: "0.0.0.0:5349", TLSCertPath: selfSigned}
		if got := reconcileTURNConfigFields(cfg, "current", "", ""); len(got) != 0 {
			t.Errorf("without a wildcard the self-signed cert must be kept, got %v", got)
		}
		if cfg.TLSCertPath != selfSigned {
			t.Errorf("cert changed despite no wildcard: %q", cfg.TLSCertPath)
		}
	})

	t.Run("TURNS disabled — cert never touched", func(t *testing.T) {
		cfg := &turn.Config{AuthSecret: "current", TURNSListenAddr: "", TLSCertPath: selfSigned}
		if got := reconcileTURNConfigFields(cfg, "current", wcCert, wcKey); len(got) != 0 {
			t.Errorf("TURNS disabled → no cert reconcile, got %v", got)
		}
	})
}
