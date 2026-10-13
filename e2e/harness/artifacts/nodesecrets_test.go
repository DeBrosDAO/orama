package artifacts

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/secrets"
)

// TestCollect_nodeSecretsMaskedEverywhere: node-1's cluster secret and the
// RQLite password inside rqlite-auth.json are masked in node-2's journal,
// though no registry knew them.
func TestCollect_nodeSecretsMaskedEverywhere(t *testing.T) {
	c := collector(t, func(node, cmd string) (fleet.Output, error) {
		switch {
		case strings.Contains(cmd, nodeSecretsDir) && node == "node-1":
			return fleet.Output{Stdout: "cs-node-secret-0001\n" + `{"user":"orama","password":"rq-pass-0002"}` + "\n"}, nil
		case strings.Contains(cmd, nodeSecretsDir):
			return fleet.Output{}, nil
		case strings.HasPrefix(cmd, "systemctl list-units"):
			return fleet.Output{Stdout: "orama-node.service loaded active running Orama\n"}, nil
		case strings.HasPrefix(cmd, "journalctl") && node == "node-2":
			return fleet.Output{Stdout: "joined with cs-node-secret-0001 as rq-pass-0002"}, nil
		}
		return fleet.Output{Stdout: "ok"}, nil
	})
	c.MaxBytes = 1 << 20
	dir := t.TempDir()
	if _, err := c.Collect(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "nodes", "node-2", "journal", "orama-node.service.log"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "cs-node-secret-0001") || strings.Contains(string(raw), "rq-pass-0002") {
		t.Fatalf("a node secret reached the artifacts: %s", raw)
	}
}

// TestCollect_unreadableSecretsWithholdTheNode: nothing is collected from a
// node whose secrets could not be read; the index says why.
func TestCollect_unreadableSecretsWithholdTheNode(t *testing.T) {
	c := collector(t, func(node, cmd string) (fleet.Output, error) {
		if strings.Contains(cmd, nodeSecretsDir) && node == "node-2" {
			return fleet.Output{}, errors.New("ssh: handshake failed")
		}
		return fleet.Output{Stdout: "ok"}, nil
	})
	dir := t.TempDir()
	ix, err := c.Collect(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range ix.Files {
		if strings.HasPrefix(f.Path, "nodes/node-2/") && f.Path != "nodes/node-2/"+withheldName {
			t.Fatalf("collected %s from a node whose secrets were not read", f.Path)
		}
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "nodes", "node-2", withheldName))
	if strings.TrimSpace(string(raw)) != secrets.Withheld {
		t.Fatalf("withheld marker %q", raw)
	}
}
