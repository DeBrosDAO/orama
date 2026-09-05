package node

import (
	"github.com/DeBrosOfficial/network/pkg/cli/production/report"
	"github.com/DeBrosOfficial/network/pkg/version"
	"github.com/spf13/cobra"
)

// The report carries the node's binary version, which is what
// `orama monitor` compares across the fleet to raise a version-mismatch alert.
// It used to be sent as the empty string, so every node reported the same
// version and the alert could never fire. The version is compiled in now, so
// there is a real value to send.
var reportCmd = &cobra.Command{
	Use:   "report",
	Short: "Output comprehensive node health data as JSON",
	Long: `Collect all system and service data from this node and output
as a single JSON blob. Designed to be called by 'orama monitor' over SSH.
Requires root privileges for full data collection.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return report.Handle(!reportPretty, version.Current)
	},
}

// reportPretty indents the report for a person reading it on the node.
//
// The output is JSON either way — `orama monitor` parses it over SSH, and the
// command exists to produce it. This used to be a `--json` flag defaulting to
// true, which meant it read as "output JSON" while only choosing the
// formatting, and setting it did nothing at all.
var reportPretty bool

func init() {
	reportCmd.Flags().BoolVar(&reportPretty, "pretty", false,
		"Indent the JSON for reading, instead of one line")
}
