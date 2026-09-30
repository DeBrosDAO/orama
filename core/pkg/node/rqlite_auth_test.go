package node

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/config"
)

// -auth is installed by the systemd spawner from the auth file copy, not
// from extra-args, so raft tuning must not carry -auth (would double it).
func TestIndexRQLiteExtraArgs_no_auth_flag(t *testing.T) {
	args := indexRQLiteExtraArgs(config.DatabaseConfig{
		RQLiteAuthFile:    "/home/orama/.orama/secrets/rqlite-auth.json",
		RQLiteEnforceAuth: true,
	})
	if strings.Contains(args, "-auth") {
		t.Fatalf("extra-args must not pass -auth: %s", args)
	}
	// rqlite v10 renamed -raft-timeout to -raft-heartbeat-timeout and rejects the old name.
	if strings.Contains(args, "-raft-timeout") {
		t.Fatalf("extra-args must not pass the removed -raft-timeout: %s", args)
	}
	for _, want := range []string{"-raft-election-timeout", "-raft-heartbeat-timeout", "-raft-apply-timeout", "-raft-leader-lease-timeout"} {
		if !strings.Contains(args, want) {
			t.Fatalf("missing %s in %s", want, args)
		}
	}
}
