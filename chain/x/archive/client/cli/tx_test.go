package cli

import (
	"testing"

	"github.com/spf13/cobra"
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
