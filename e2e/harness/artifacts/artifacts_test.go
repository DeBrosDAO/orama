package artifacts

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/secrets"
)

type scriptShell struct {
	answer func(cmd string) (fleet.Output, error)
}

func (s *scriptShell) Run(_ context.Context, cmd string) (fleet.Output, error) { return s.answer(cmd) }
func (s *scriptShell) Put(context.Context, string, []byte, os.FileMode) error  { return nil }
func (s *scriptShell) Get(context.Context, string) ([]byte, error)             { return nil, nil }

func collector(t *testing.T, answer func(node, cmd string) (fleet.Output, error)) *FleetCollector {
	t.Helper()
	return collectorFor(t, &fleet.State{RunID: "r", Env: "e2e-r", ChainID: "orama-devnet-e2e-r",
		Nodes: []fleet.Node{{Name: "node-1"}, {Name: "node-2"}}}, answer)
}

func collectorFor(t *testing.T, st *fleet.State, answer func(node, cmd string) (fleet.Output, error)) *FleetCollector {
	t.Helper()
	f := fleet.NewWithDialer(st, nil, func(_ fleet.State, n fleet.Node) fleet.Shell {
		return &scriptShell{answer: func(cmd string) (fleet.Output, error) { return answer(n.Name, cmd) }}
	})
	return &FleetCollector{Fleet: f, Since: time.Unix(1790000000, 0), MaxBytes: 64,
		Redactor: secrets.NewRedactor("super-secret-cluster-key")}
}

func TestCollect_nodesJournalsBoundsAndRedaction(t *testing.T) {
	c := collector(t, func(node, cmd string) (fleet.Output, error) {
		switch {
		case strings.HasPrefix(cmd, "systemctl list-units"):
			return fleet.Output{Stdout: "orama-node.service loaded active running Orama\ncaddy.service loaded active running Caddy\n"}, nil
		case strings.HasPrefix(cmd, "journalctl -u orama-node.service"):
			return fleet.Output{Stdout: strings.Repeat("x", 200)}, nil
		case strings.HasPrefix(cmd, "orama node report"):
			return fleet.Output{Stdout: `{"secret":"super-secret-cluster-key"}`}, nil
		case strings.HasPrefix(cmd, "curl"):
			return fleet.Output{Exit: 7, Stderr: "connection refused"}, nil
		case node == "node-2" && strings.HasPrefix(cmd, "wg show"):
			return fleet.Output{}, errors.New("ssh: handshake failed")
		}
		return fleet.Output{Stdout: "ok"}, nil
	})
	dir := t.TempDir()
	ix, err := c.Collect(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]File{}
	for _, f := range ix.Files {
		byPath[f.Path] = f
	}
	j := byPath["nodes/node-1/journal/orama-node.service.log"]
	if !j.Truncated || j.Bytes > 64+len(truncatedMarker) || !strings.Contains(j.Command, "--since @1790000000") {
		t.Fatalf("journal %+v", j)
	}
	if _, ok := byPath["nodes/node-2/journal/caddy.service.log"]; !ok {
		t.Fatal("caddy journal missing")
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "nodes/node-1/node-report.json"))
	if strings.Contains(string(raw), "super-secret-cluster-key") {
		t.Fatal("secret written to an artifact")
	}
	if byPath["nodes/node-1/chain-status.json"].Error != "exit 7" || byPath["nodes/node-2/wireguard.txt"].Error == "" {
		t.Fatalf("errors not recorded: %+v %+v", byPath["nodes/node-1/chain-status.json"], byPath["nodes/node-2/wireguard.txt"])
	}
	if len(ix.Failed()) != 3 {
		t.Fatalf("failed items %+v", ix.Failed())
	}
	loaded, err := LoadIndex(dir)
	if err != nil || len(loaded.Files) != len(ix.Files) {
		t.Fatalf("index round trip: %v", err)
	}
}

func TestCollect_unwritableDir(t *testing.T) {
	c := collector(t, func(string, string) (fleet.Output, error) { return fleet.Output{}, nil })
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Collect(context.Background(), filepath.Join(file, "sub")); err == nil {
		t.Fatal("writing under a file succeeded")
	}
}

func TestParseUnits_filtersNoise(t *testing.T) {
	got := ParseUnits("orama-node.service loaded active\n\n● bad unit\nwg-quick@wg0.service loaded\n")
	if strings.Join(got, ",") != "orama-node.service,wg-quick@wg0.service" {
		t.Fatalf("got %v", got)
	}
	if len(ParseUnits("")) != 0 {
		t.Fatal("units from empty output")
	}
}

func TestLoadIndex_missing(t *testing.T) {
	if _, err := LoadIndex(t.TempDir()); err == nil {
		t.Fatal("missing index accepted")
	}
}

func TestCollect_chainStatusOnlyWithChain(t *testing.T) {
	st := &fleet.State{RunID: "r", Env: "e2e-r", Nodes: []fleet.Node{{Name: "node-1"}}}
	c := collectorFor(t, st, func(string, string) (fleet.Output, error) { return fleet.Output{Stdout: "ok"}, nil })
	ix, err := c.Collect(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range ix.Files {
		if strings.HasSuffix(f.Path, "chain-status.json") {
			t.Fatalf("chain status collected on a run without a chain: %+v", f)
		}
	}
}

func TestCollect_refusesNodeNameOutsideDir(t *testing.T) {
	st := &fleet.State{RunID: "r", Env: "e2e-r", Nodes: []fleet.Node{{Name: "../../escape"}, {Name: "node-1"}}}
	c := collectorFor(t, st, func(string, string) (fleet.Output, error) { return fleet.Output{Stdout: "ok"}, nil })
	root := t.TempDir()
	dir := filepath.Join(root, "collected")
	ix, err := c.Collect(context.Background(), dir)
	if err == nil || !strings.Contains(err.Error(), "not a node name") {
		t.Fatalf("err %v", err)
	}
	for _, f := range ix.Files {
		if strings.Contains(f.Path, "..") {
			t.Fatalf("collected %s", f.Path)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "escape")); !os.IsNotExist(err) {
		t.Fatalf("wrote outside the collection dir: %v", err)
	}
}
