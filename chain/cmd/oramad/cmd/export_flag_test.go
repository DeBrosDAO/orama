package cmd

import (
	"testing"

	"github.com/spf13/cobra"

	"github.com/DeBrosOfficial/network/chain/app"
)

// `oramad export` carries the explicit opt-in for exporting a state that breaks conservation, and it
// defaults to off: the refusal is the default.
func TestExportCommand_hasTheConservationOptInFlagOffByDefault(t *testing.T) {
	root := &cobra.Command{Use: "oramad"}
	root.AddCommand(&cobra.Command{Use: "export"})
	addExportFlags(root)
	f := root.Commands()[0].Flags().Lookup(app.FlagAllowConservationViolation)
	if f == nil {
		t.Fatalf("oramad export has no --%s flag", app.FlagAllowConservationViolation)
	}
	if f.DefValue != "false" {
		t.Fatalf("--%s defaults to %s, want false", app.FlagAllowConservationViolation, f.DefValue)
	}
}
