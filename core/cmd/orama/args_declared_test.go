package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/spf13/cobra"
)

// A command whose Use names no positional argument takes none, and has to say
// so with Args: cobra.NoArgs. Left nil, the arity check never runs: a stray
// word reaches the handler, which looks up credentials or contacts the gateway
// first and answers with that failure instead of "unknown argument" (stagenet
// e2e, 2026-09-30: `orama auth sessions <typo>` exited 3, no credentials,
// instead of 2).
func TestAuthCommandsTakingNoArgumentsDeclareNoArgs(t *testing.T) {
	auth, _, err := newRootCmd().Find([]string{"auth"})
	if err != nil || auth.Name() != "auth" {
		t.Fatalf("the auth group was not found: %v", err)
	}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			walk(sub)
		}
		takesNone := !strings.ContainsAny(c.Use, " <[")
		if c != auth && c.Runnable() && takesNone && c.Args == nil {
			t.Errorf("`%s` takes no arguments but declares no Args: add Args: cobra.NoArgs", c.CommandPath())
		}
	}
	walk(auth)
}

// The arity check runs before the handler, so a stray word is a usage error
// whether or not the machine holds a credential.
func TestAuthCommands_strayArgumentIsUsageBeforeAnyCredentialLookup(t *testing.T) {
	// An environment is configured and no credential is stored, so a handler
	// that runs fails with the no-credentials code, not with usage.
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, name := range []string{"ORAMA_API_URL", "ORAMA_GATEWAY_URL", "ORAMA_GATEWAY", "ORAMA_TOKEN"} {
		t.Setenv(name, "")
	}
	if err := os.MkdirAll(filepath.Join(home, ".orama"), 0o700); err != nil {
		t.Fatal(err)
	}
	envs := `{"environments":[{"name":"devnet","gateway_url":"https://gateway.invalid"}],"active_environment":"devnet"}`
	if err := os.WriteFile(filepath.Join(home, ".orama", "environments.json"), []byte(envs), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"auth", "sessions", "e2e-no-such-subcommand"},
		{"auth", "whoami", "extra"},
		{"auth", "logout", "extra"},
		{"auth", "status", "extra"},
		{"auth", "list", "extra"},
		{"auth", "switch", "extra"},
		{"auth", "login", "extra"},
	} {
		root := newRootCmd()
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs(args)
		err := root.Execute()
		if got := clierr.CodeOf(err); err == nil || got != clierr.CodeUsage {
			t.Errorf("%v: exit code %d (%v), want %d", args, got, err, clierr.CodeUsage)
		}
	}
}
