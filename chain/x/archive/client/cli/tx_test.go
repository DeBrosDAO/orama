package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"

	"github.com/DeBrosOfficial/network/chain/piece"
)

// MsgAttest and MsgAttachReplicas fail ValidateBasic without a node id, and
// neither command used to set one, so both always failed.
func TestTxCommands_requireNodeID(t *testing.T) {
	for _, cmd := range []*cobra.Command{GetCmdAttest(), GetCmdAttachReplicas()} {
		f := cmd.Flags().Lookup(flagNodeID)
		if f == nil {
			t.Fatalf("%s has no --%s flag", cmd.Name(), flagNodeID)
		}
		if got := f.Annotations[cobra.BashCompOneRequiredFlag]; len(got) != 1 || got[0] != "true" {
			t.Errorf("%s: --%s is not required", cmd.Name(), flagNodeID)
		}
	}
}

func TestAttest_requiresTheBundleFileItCommitsTo(t *testing.T) {
	f := GetCmdAttest().Flags().Lookup(flagBundleFile)
	if f == nil {
		t.Fatalf("attest has no --%s flag", flagBundleFile)
	}
	if got := f.Annotations[cobra.BashCompOneRequiredFlag]; len(got) != 1 || got[0] != "true" {
		t.Errorf("--%s is not required", flagBundleFile)
	}
}

func TestCommitBundleFile(t *testing.T) {
	dir := t.TempDir()
	body := make([]byte, 3000)
	body[0], body[2999] = 1, 2
	path := filepath.Join(dir, "1-100.orbh")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := commitBundleFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want, err := piece.Commit(body)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Root) != string(want.Root) || got.RealLeafCount != 3 || got.PaddedLeafCount != 4 || got.PieceBytes != 3000 {
		t.Errorf("commitment %+v does not match piece.Commit %+v", got, want)
	}
	if err := got.Validate(); err != nil {
		t.Errorf("a committed file must pass the message's own shape check: %v", err)
	}

	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for name, p := range map[string]string{"missing": filepath.Join(dir, "nope"), "empty": empty, "directory": dir} {
		if _, err := commitBundleFile(p); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}
