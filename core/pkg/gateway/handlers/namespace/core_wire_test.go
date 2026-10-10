package namespace

import (
	"context"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway"
)

// The orama directory and the cluster secret both come from
// cluster_secret_path. The directory used to fall back to $HOME/.orama, which
// the unit cannot see, and a missing secret spawned namespace gateways without
// cluster_secret_path — gateways that then had no node identity.
func TestWireCoreGateway_requiresTheNodesDirectoryAndSecret(t *testing.T) {
	for name, tc := range map[string]struct {
		cfg  gateway.Config
		want string
	}{
		"no orama directory": {gateway.Config{ClusterSecret: "s"}, "no orama directory"},
		"no cluster secret":  {gateway.Config{DataDir: t.TempDir()}, "no cluster secret"},
	} {
		t.Run(name, func(t *testing.T) {
			err := WireCoreGateway(context.Background(), &gateway.Gateway{}, &tc.cfg, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

// With both present the checks pass; the zero Gateway then stops at its
// missing ORM client, which is past them.
func TestWireCoreGateway_directoryAndSecretPresent(t *testing.T) {
	cfg := gateway.Config{DataDir: t.TempDir(), ClusterSecret: "s"}
	err := WireCoreGateway(context.Background(), &gateway.Gateway{}, &cfg, nil)
	if err == nil || !strings.Contains(err.Error(), "no ORM client") {
		t.Fatalf("got %v, want the ORM client check to be reached", err)
	}
}
