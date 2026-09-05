package node

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/config"
)

// Credentials alone must NOT start rqlited with -auth.
//
// This is the property that makes the rollout safe: a node can be given
// credentials — and start sending them — while still accepting the
// unauthenticated calls every peer on the previous release is making.
func TestIndexRQLiteExtraArgs_auth_file_alone_does_not_enforce(t *testing.T) {
	args := indexRQLiteExtraArgs(config.DatabaseConfig{
		RQLiteAuthFile: "/home/orama/.orama/secrets/rqlite-auth.json",
	})
	if strings.Contains(args, "-auth") {
		t.Fatalf("credentials alone switched enforcement on: %s", args)
	}
}

func TestIndexRQLiteExtraArgs_enforce_passes_auth_flag(t *testing.T) {
	const path = "/home/orama/.orama/secrets/rqlite-auth.json"
	args := indexRQLiteExtraArgs(config.DatabaseConfig{
		RQLiteAuthFile:    path,
		RQLiteEnforceAuth: true,
	})
	if !strings.Contains(args, "-auth "+path) {
		t.Fatalf("enforcement did not pass the auth file: %s", args)
	}
}

func TestIndexRQLiteExtraArgs_no_auth_config_at_all(t *testing.T) {
	args := indexRQLiteExtraArgs(config.DatabaseConfig{})
	if strings.Contains(args, "-auth") {
		t.Fatalf("unexpected -auth: %s", args)
	}
	// The raft tuning flags are unconditional and must survive.
	for _, want := range []string{"-raft-election-timeout", "-raft-timeout", "-raft-apply-timeout", "-raft-leader-lease-timeout"} {
		if !strings.Contains(args, want) {
			t.Fatalf("missing %s in %s", want, args)
		}
	}
}
