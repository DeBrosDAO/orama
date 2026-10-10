package globalcmd

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/cmdmeta"
	"github.com/DeBrosOfficial/network/pkg/globalnode"
	"github.com/DeBrosOfficial/network/pkg/install"
	"github.com/spf13/cobra"
)

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Move the validator key to another host without a double sign",
	Long: `Move priv_validator_key.json and priv_validator_state.json from this host to
another, in three steps, each run as root:

  1. on the new host:  orama maint global validator migrate prepare
  2. on the old host:  orama maint global validator migrate export --recipient <key> --to <file>
  3. copy <file> to the new host, then:
                       orama maint global validator migrate import --from <file>

export stops and disables the old host's chain (and stops the services that
need it) before it reads anything. It seals the key and state in memory, records
the state as the old host's sign floor, keeps a copy of the state, moves the key
out of the chain home, and then writes the bundle. The chain unit checks the
floor before every start, so the old host's chain no longer starts: the floor is
recorded and the key is gone. import refuses while the new host's chain runs,
records the old host's last sign state as the new host's floor, writes the
state, and installs the key last; the chain unit then refuses to start from a
state behind the floor. cancel removes a prepared migration key.

A bundle from 'orama maint global validator reseal' (a restored backup) has no sign
state. Its import needs --old-host-destroyed and --floor-height with the
network's latest committed height H. The floor and the state become height H+1,
round 0, before any step: the restored key signs nothing at or below H, in any
round. It can sign at H+1, so a vote the lost host cast at H+1 is excluded only
if that host stopped before H+1 began.`,
}

var migratePrepareCmd = &cobra.Command{
	Use:   "prepare",
	Short: "Print this host's migration key (run on the new host)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if err := clierr.RequireRoot("preparing a validator migration"); err != nil {
			return err
		}
		pub, err := globalnode.DefaultHost().PrepareMigration()
		if err != nil {
			return clierr.Failure("%v", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s\n", hex.EncodeToString(pub[:]))
		return nil
	},
}

var migrateCancelCmd = &cobra.Command{
	Use:   "cancel",
	Short: "Remove this host's prepared migration key (run on the new host)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if err := clierr.RequireRoot("cancelling a validator migration"); err != nil {
			return err
		}
		removed, err := globalnode.DefaultHost().CancelMigration()
		if err != nil {
			return clierr.Failure("%v", err)
		}
		if !removed {
			fmt.Fprintf(cmd.OutOrStdout(), "no migration was prepared on this host\n")
			return nil
		}
		fmt.Fprintf(cmd.OutOrStdout(), "migration key removed\n")
		return nil
	},
}

var checkSignFloorCmd = &cobra.Command{
	Use:   "check-sign-floor",
	Short: "Fail when the chain must not start: key moved away or state behind its floor",
	Long: `The double-sign guard. orama-global-chain.service runs it as root before every
start (ExecStartPre), from /usr/lib/orama-global/bin, where 'orama global
install' puts this CLI. With no sign floor recorded it passes. With one, it
fails when priv_validator_key.json is missing (the key moved to another host)
or priv_validator_state.json is behind the floor.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if err := clierr.RequireRoot("checking the sign floor"); err != nil {
			return err
		}
		if err := globalnode.DefaultHost().CheckSignFloor(); err != nil {
			return clierr.Conflict("refusing to start the chain: %v", err)
		}
		return nil
	},
}

var migrateExportFlags struct{ recipient, to string }

var migrateExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Stop the chain and seal the key and its sign state (run on the old host)",
	Args:  cobra.NoArgs,
	RunE:  runMigrateExport,
}

var migrateImportFlags struct {
	from             string
	oldHostDestroyed bool
	floorHeight      int64
}

var migrateImportCmd = &cobra.Command{
	Use:   "import",
	Short: "Install a migrated key and record its sign floor (run on the new host)",
	Args:  cobra.NoArgs,
	RunE:  runMigrateImport,
}

func init() {
	e := migrateExportCmd.Flags()
	e.StringVar(&migrateExportFlags.recipient, "recipient", "", "The new host's migration key, from prepare [required]")
	e.StringVar(&migrateExportFlags.to, "to", "", "Bundle file to write; must not exist [required]")
	i := migrateImportCmd.Flags()
	i.StringVar(&migrateImportFlags.from, "from", "", "Bundle file from export or reseal [required]")
	i.BoolVar(&migrateImportFlags.oldHostDestroyed, "old-host-destroyed", false, "For a reseal bundle: confirm the old host can never start again")
	i.Int64Var(&migrateImportFlags.floorHeight, "floor-height", 0, "For a reseal bundle: the network's latest committed height; the key signs only above it")
	migrateCmd.AddCommand(migratePrepareCmd, migrateExportCmd, migrateImportCmd, migrateCancelCmd)
	validatorCmd.AddCommand(cmdmeta.MarkNodeLocal(checkSignFloorCmd))
	validatorCmd.AddCommand(migrateCmd)
	// orama-global-chain.service runs `orama global validator check-sign-floor`
	// (install.GlobalSignFloorCheck) as root before every start, with no home and
	// no operator environment. The command moved to `orama maint global validator`;
	// the unit keeps its path, and an installed unit keeps it until an upgrade
	// rewrites it, so the old path stays as a hidden alias.
	unitPath := &cobra.Command{
		Use:    validatorCmd.Use,
		Short:  "Kept for installed units: orama maint global validator",
		Hidden: true,
	}
	unitPath.AddCommand(cmdmeta.HiddenAlias(checkSignFloorCmd))
	Cmd.AddCommand(unitPath)
}

func runMigrateExport(cmd *cobra.Command, _ []string) error {
	recipient, err := globalnode.ParseX25519Hex(migrateExportFlags.recipient)
	if err != nil || migrateExportFlags.to == "" {
		return clierr.Usage("--recipient (64 hex characters) and --to are required")
	}
	if _, err := os.Lstat(migrateExportFlags.to); !errors.Is(err, fs.ErrNotExist) {
		return clierr.Usage("--to %s must not exist", migrateExportFlags.to)
	}
	if err := clierr.RequireRoot("exporting the validator key"); err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	life := globalnode.DefaultLifecycle(out)
	if err := life.Stop([]install.GlobalService{install.GlobalServiceChain}); err != nil {
		return clierr.Failure("stop the chain before the key moves: %v", err)
	}
	if err := life.ChainStopped(); err != nil {
		return clierr.Failure("%v", err)
	}
	if err := life.DisableChain(); err != nil {
		return clierr.Failure("%v", err)
	}
	bundle, exported, err := globalnode.DefaultHost().ExportMigration(recipient, life.ChainStopped)
	if err != nil {
		return clierr.Failure("%v", err)
	}
	if err := writeNewFile(cmd, migrateExportFlags.to, bundle); err != nil {
		return clierr.Failure("the key left the chain home (copy %s, state copy %s) but the bundle was not written; put both back to abandon: %v",
			exported.KeyCopy, exported.StateCopy, err)
	}
	fmt.Fprintf(out, "chain stopped and disabled at %s\nkey moved to %s, state copied to %s\ncopy %s to the new host and run the migrate import step there\n",
		exported.State, exported.KeyCopy, exported.StateCopy, migrateExportFlags.to)
	return nil
}

func runMigrateImport(cmd *cobra.Command, _ []string) error {
	restore, err := restoreFloor()
	if err != nil {
		return err
	}
	if err := clierr.RequireRoot("importing a validator key"); err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if err := globalnode.DefaultLifecycle(out).ChainStopped(); err != nil {
		return clierr.Conflict("%v", err)
	}
	blob, err := os.ReadFile(migrateImportFlags.from)
	if err != nil {
		return clierr.Failure("read %s: %w", migrateImportFlags.from, err)
	}
	res, err := globalnode.DefaultHost().ImportMigration(blob, restore)
	if err != nil {
		return clierr.Failure("%v", err)
	}
	if res.Replaced != "" {
		fmt.Fprintf(out, "the key that was here moved to %s\n", res.Replaced)
	}
	fmt.Fprintf(out, "sign floor recorded at %s\n", *res.Floor)
	fmt.Fprintf(out, "start the chain with: orama global start\n")
	return nil
}

// restoreFloor reads the import flags. A reseal bundle needs both restore
// flags; a migration bundle takes neither, which ImportMigration checks
// against what the bundle carries.
func restoreFloor() (*globalnode.SignState, error) {
	if migrateImportFlags.from == "" {
		return nil, clierr.Usage("--from is required")
	}
	if migrateImportFlags.oldHostDestroyed != (migrateImportFlags.floorHeight != 0) {
		return nil, clierr.Usage("--old-host-destroyed and --floor-height go together, and only with a reseal bundle")
	}
	if !migrateImportFlags.oldHostDestroyed {
		return nil, nil
	}
	floor, err := globalnode.RestoreFloor(migrateImportFlags.floorHeight)
	if err != nil {
		return nil, clierr.Usage("%v", err)
	}
	return &floor, nil
}

// warnIfMigratedAway prints a warning when this host's validator key was
// migrated away. The chain unit's ExecStartPre check is what refuses a start.
// It says nothing when not run as root, which cannot read the state root.
func warnIfMigratedAway(cmd *cobra.Command) {
	if os.Geteuid() != 0 {
		return
	}
	away, err := globalnode.DefaultHost().MigratedAway()
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not check the sign floor: %v\n", err)
		return
	}
	if away {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: this host's validator key was migrated to another host; the chain will refuse to start here\n")
	}
}
