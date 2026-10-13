package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStart_passwordShreddedOnceReadyAndSecretsRegistered: the password
// file is gone once the agent serves (it read it at start), and the
// password and mnemonic reached the caller's redactor first.
func TestStart_passwordShreddedOnceReadyAndSecretsRegistered(t *testing.T) {
	fakeHome(t)
	cfg := startCfg(t, modeOK, okRW)
	var got []string
	cfg.Redact = func(values ...string) error { got = append(got, values...); return nil }
	a, err := Start(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Stop()
	if _, err := os.Lstat(filepath.Join(a.Dir, passwordName)); !os.IsNotExist(err) {
		t.Fatalf("the password file outlived the agent's start: %v", err)
	}
	if len(got) != 2 || len(got[0]) < passwordBytes || len(strings.Fields(got[1])) != 12 {
		t.Fatalf("registered %d values", len(got))
	}
}

func TestStart_redactRegistrationFailureStops(t *testing.T) {
	fakeHome(t)
	cfg := startCfg(t, modeOK, okRW)
	cfg.Redact = func(...string) error { return os.ErrPermission }
	if _, err := Start(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "redaction") {
		t.Fatalf("err %v", err)
	}
	assertNoAgentDirs(t, cfg.BaseDir)
}
