package globalcmd

import (
	"fmt"
	"os"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/cosmovisor"
	"github.com/DeBrosOfficial/network/pkg/releaseverify"
	"github.com/spf13/cobra"
)

var stageFlags struct {
	binary   string
	metadata string
	target   string
	upgrade  string
	genesis  bool
	home     string
}

var stageOramadCmd = &cobra.Command{
	Use:   "stage-oramad",
	Short: "Place a TUF-verified oramad in the cosmovisor layout",
	Long: `Place an oramad binary where cosmovisor runs it, after it verifies against
the release root adopted at /etc/orama/release-root.json.

--upgrade <name> stages <home>/cosmovisor/upgrades/<name>/bin/oramad for the
upgrade plan <name>; cosmovisor switches to it at the plan's height. --genesis
stages <home>/cosmovisor/genesis/bin/oramad and points current at genesis if
current does not exist yet. A binary already there is refused.

The binary is copied into a root-only staging directory and verified there,
through the descriptor that wrote it, as --release-target in the TUF metadata
in --release-metadata (threshold, timestamp expiry, snapshot rollback, length
and hashes); only then is it linked into place. Every directory on the way is
opened without following symlinks and must be root's; a symlink or a
directory another account owns or may write is refused. Nothing stages
automatically: a validator's operator runs this for every chain upgrade.

The chain unit 'orama global install' writes runs oramad directly, not through
cosmovisor, and does not read this layout.`,
	Args: cobra.NoArgs,
	RunE: runStageOramad,
}

func init() {
	f := stageOramadCmd.Flags()
	f.StringVar(&stageFlags.binary, "binary", "", "The oramad binary to stage [required]")
	f.StringVar(&stageFlags.metadata, "release-metadata", "", "Directory holding timestamp.json, snapshot.json and targets.json [required]")
	f.StringVar(&stageFlags.target, "release-target", "", "Name the binary has in the release targets metadata [required]")
	f.StringVar(&stageFlags.upgrade, "upgrade", "", "Upgrade plan name to stage for")
	f.BoolVar(&stageFlags.genesis, "genesis", false, "Stage the genesis binary instead of an upgrade")
	f.StringVar(&stageFlags.home, "home", constants.ChainHome, "cosmovisor DAEMON_HOME")
	Cmd.AddCommand(stageOramadCmd)
}

func runStageOramad(cmd *cobra.Command, _ []string) error {
	if stageFlags.binary == "" || stageFlags.metadata == "" || stageFlags.target == "" {
		return clierr.Usage("--binary, --release-metadata and --release-target are required")
	}
	if stageFlags.genesis == (stageFlags.upgrade != "") {
		return clierr.Usage("give exactly one of --upgrade <name> and --genesis")
	}
	if err := clierr.RequireRoot("staging oramad"); err != nil {
		return err
	}
	uid, gid, err := cosmovisor.LookupAccount(constants.ChainUser)
	if err != nil {
		return clierr.Failure("%v", err)
	}
	layout := cosmovisor.Layout{
		Home:     stageFlags.home,
		Daemon:   constants.ChainDaemonName,
		ChainUID: uid,
		ChainGID: gid,
	}
	verify := func(f *os.File) error {
		_, err := releaseverify.CheckFile(releaseverify.FileCheck{
			RootPath:    releaseverify.RootPath,
			SeenPath:    releaseverify.SeenPath,
			MetadataDir: stageFlags.metadata,
			Target:      stageFlags.target,
			File:        f,
			Now:         time.Now(),
		})
		return err
	}
	var dst string
	if stageFlags.genesis {
		dst, err = layout.StageGenesis(stageFlags.binary, verify)
	} else {
		dst, err = layout.StageUpgrade(stageFlags.upgrade, stageFlags.binary, verify)
	}
	if err != nil {
		return clierr.Failure("%v", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "staged %s\n", dst)
	return nil
}
