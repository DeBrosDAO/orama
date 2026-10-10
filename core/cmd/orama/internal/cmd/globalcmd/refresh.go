package globalcmd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/cmdmeta"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/globalnetns"
	"github.com/DeBrosOfficial/network/pkg/globalnode"
	"github.com/DeBrosOfficial/network/pkg/install"
	"github.com/spf13/cobra"
)

const (
	// releaseBinDir is the release `orama upgrade` staged: its bin/ holds the
	// binaries the refresh installs.
	releaseBinDir = "/opt/orama/bin"
	// planQueryBudget bounds the read of the chain's scheduled upgrade.
	planQueryBudget = 15 * time.Second
)

var refreshFlags struct {
	stagedDir string
	manifest  string
}

var refreshCmd = &cobra.Command{
	Use:   "refresh",
	Short: "Bring the installed global services up to the staged release (run as root)",
	Long: `Put the staged release's global binaries in place of the installed ones and restart
the services that run them. 'orama upgrade' runs it on every node that has the
global layer, after the node's cluster services are upgraded, one node at a time.

Each binary the installed services need (orama, orama-global, ipfs) is read from
--staged-dir, held to the release manifest, and replaced atomically when its bytes
differ. A service is restarted only if the binary its own process runs was
replaced: the provider, archiver, indexer, repair delegate and reporter run
orama-global, the public Kubo runs ipfs, the onion service's gate runs orama. The
chain and the Tor relay or directory authority are never restarted by a refresh
(the chain runs oramad from the cosmovisor layout; Tor runs the distro's tor).

oramad changes only through a governed upgrade. When the release carries an oramad
other than the one cosmovisor runs, the refresh reads the chain's scheduled
upgrade plan: with one, it stages the release's oramad and shielded verifier for
that plan, and cosmovisor switches to them at the plan's height; without one, it
keeps the running oramad and says so. When the plan's info names a checksum for
this platform's binary, the release's oramad has to be that one or it is not
staged.

A service is restarted when its running process executes a file that has been
replaced (/proc/<pid>/exe is "(deleted)"), not because of what this run replaced:
a refresh that was interrupted is finished by running it again. A service that is
not running is not started by it.`,
	Args: cobra.NoArgs,
	RunE: runRefresh,
}

func init() {
	f := refreshCmd.Flags()
	f.StringVar(&refreshFlags.stagedDir, "staged-dir", releaseBinDir, "The staged release's bin/ directory")
	f.StringVar(&refreshFlags.manifest, "manifest", install.DefaultStagedManifest, "The staged release's manifest.json")
	MaintCmd.AddCommand(cmdmeta.MarkNodeLocal(refreshCmd))
}

func runRefresh(cmd *cobra.Command, _ []string) error {
	if err := clierr.RequireRoot("refreshing the global layer"); err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	life := globalnode.DefaultLifecycle(out)
	installed, err := life.Installed()
	if err != nil {
		return clierr.Failure("%v", err)
	}
	host := install.DefaultGlobalHost(func(format string, args ...any) { fmt.Fprintf(out, format+"\n", args...) })
	opts := install.RefreshOptions{Installed: installed, StagedDir: refreshFlags.stagedDir, Manifest: refreshFlags.manifest}
	res, err := install.RefreshGlobal(host, opts)
	if err != nil {
		return clierr.Failure("%v", err)
	}
	if len(res.Restart) == 0 {
		fmt.Fprintln(out, "  global binaries: no running service needs a restart")
	} else if err := life.Restart(cmd.Context(), res.Restart); err != nil {
		return clierr.Failure("restart the services that run a replaced binary: %v", err)
	}
	if res.Chain == nil {
		return nil
	}
	base := fmt.Sprintf("http://%s:%d", globalnetns.ChainHost(install.ChainColocated(life.UnitDir)), constants.ChainAPIPort)
	return handleChainBinary(cmd.Context(), out, host, opts, *res.Chain, base)
}

// handleChainBinary stages the release's oramad when the chain has a governed
// upgrade scheduled and the release carries another one, and otherwise says
// why the running oramad was kept.
func handleChainBinary(ctx context.Context, out io.Writer, host install.GlobalHost, opts install.RefreshOptions, chain install.ChainBinaryState, restBase string) error {
	if !chain.Differs() {
		fmt.Fprintln(out, "  chain: the release carries the oramad the chain runs; nothing to stage")
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, planQueryBudget)
	defer cancel()
	plan, err := globalnode.CurrentUpgradePlan(ctx, restBase, &http.Client{Timeout: planQueryBudget})
	if err != nil {
		return clierr.Failure("the release carries a different oramad (%s) and the chain's scheduled upgrade could not be read, so the chain binary is undecided: %v",
			short(chain.ReleaseSHA256), err)
	}
	if plan.Name == "" {
		fmt.Fprintf(out, "  chain: oramad kept. The release carries another one (%s, the chain runs %s), but no governed upgrade is scheduled, "+
			"and a chain binary changes only at a governed upgrade. When a proposal passes, run 'orama upgrade' again to stage it.\n",
			short(chain.ReleaseSHA256), short(chain.CurrentSHA256))
		return nil
	}
	named, err := checkPlanNamesThisBinary(out, plan, chain)
	if err != nil {
		return err
	}
	dst, err := install.StageChainUpgrade(host, opts, plan.Name)
	if err != nil {
		return clierr.Failure("%v", err)
	}
	fmt.Fprintf(out, "  chain: the release's oramad staged for the scheduled upgrade %q at %s; cosmovisor switches to it at the plan's height%s\n", plan.Name, dst, named)
	return nil
}

// checkPlanNamesThisBinary holds the release's oramad to the checksum the
// scheduled plan names for this platform, when it names one: a binary the
// proposal does not name is not staged, because cosmovisor would switch the
// validator to it at the plan's height. A plan that names none is allowed, and
// the output says it was not checked.
func checkPlanNamesThisBinary(out io.Writer, plan globalnode.UpgradePlan, chain install.ChainBinaryState) (note string, err error) {
	sum, named := plan.BinaryChecksum(runtime.GOOS + "/" + runtime.GOARCH)
	switch {
	case !named:
		fmt.Fprintf(out, "  chain: the scheduled upgrade %q names no checksum for this platform's binary, so the release's oramad (%s) could not be checked against it\n", plan.Name, short(chain.ReleaseSHA256))
		return "", nil
	case sum != chain.ReleaseSHA256:
		return "", clierr.Conflict("the scheduled upgrade %q names an oramad with sha256 %s, but the release carries %s: this release is not the one the proposal was for, so it is not staged",
			plan.Name, short(sum), short(chain.ReleaseSHA256))
	}
	return " (its checksum is the one the plan names)", nil
}

// short is the first digits of a digest, enough to tell two binaries apart.
func short(sum string) string {
	const digits = 12
	if len(sum) > digits {
		return sum[:digits]
	}
	return sum
}
