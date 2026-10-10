package statuscmd

import (
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor/display"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/printer"
	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
)

// View is a subcommand that reads one snapshot and prints one view of it. `orama status` mounts each as a
// subcommand.
type View struct {
	Use, Short string
	Table      func(*cluster.ClusterSnapshot, io.Writer) error
	JSON       func(*cluster.ClusterSnapshot, io.Writer) error
}

// Views are the one-shot views of a snapshot.
var Views = []View{
	{Use: "cluster", Short: "Verdict, components and a row per node (one-shot)", Table: display.ClusterTable, JSON: display.ClusterJSON},
	{Use: "node", Short: "Per-node health details (one-shot)", Table: display.NodeTable, JSON: display.NodeJSON},
	{Use: "service", Short: "Service status across the cluster (one-shot)", Table: display.ServiceTable, JSON: display.ServiceJSON},
	{Use: "mesh", Short: "WireGuard mesh connectivity (one-shot)", Table: display.MeshTable, JSON: display.MeshJSON},
	{Use: "dns", Short: "DNS and TLS health of the nameservers (one-shot)", Table: display.DNSTable, JSON: display.DNSJSON},
	{Use: "namespaces", Short: "Namespace health across nodes (one-shot)", Table: display.NamespacesTable, JSON: display.NamespacesJSON},
	{Use: "alerts", Short: "Alerts, most severe first, with what to do (one-shot)", Table: display.AlertsTable, JSON: display.AlertsJSON},
	{Use: "traffic", Short: "Gateway requests, errors and latency (one-shot)", Table: display.TrafficTable, JSON: display.TrafficJSON},
	{Use: "chain", Short: "Orama L1 height, sync and validators (one-shot)", Table: display.ChainTable, JSON: display.ChainJSON},
	{Use: "report", Short: "Full cluster report as JSON (one-shot)", Table: display.FullReport, JSON: display.FullReport},
}

// ViewCommand is the subcommand that prints v of the snapshot source makes.
func ViewCommand(v View, source func() (monitor.Source, error)) *cobra.Command {
	return &cobra.Command{
		Use:   v.Use,
		Short: v.Short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			src, err := source()
			if err != nil {
				return err
			}
			snap, err := src.Snapshot(cmd.Context())
			if err != nil {
				return err
			}
			if printer.For(cmd).JSONMode() {
				return v.JSON(snap, os.Stdout)
			}
			return v.Table(snap, os.Stdout)
		},
	}
}
