package globalcmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/cosmovisor"
	"github.com/DeBrosOfficial/network/pkg/globalnode"
	"github.com/DeBrosOfficial/network/pkg/install"
	"github.com/DeBrosOfficial/network/pkg/planinfo"
	"github.com/DeBrosOfficial/network/pkg/releaseverify"
	"github.com/spf13/cobra"
)

var stageFlags struct {
	binary   string
	metadata string
	target   string
	// verifier and verifierTarget are the shielded verifier of the same
	// release and its name in the targets metadata.
	verifier       string
	verifierTarget string
	upgrade        string
	planInfo       string
	genesis        bool
	home           string
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

For an upgrade, oramad and the verifier are also held to the checksums the plan
names: the sha256 of this platform's oramad and shielded verifier, which the
SoftwareUpgrade proposal carried in its info and x/upgrade holds as the plan's
info. The plan is read from the chain's REST API and has to be the one named
--upgrade; --plan-info <file> reads the info JSON from a file instead, for a
machine whose chain cannot be asked. The sha256 is taken of the staged copy, in
the same pass that verifies it, so a file changed after the command started is
the file that is checked. A file the plan does not name, or a plan that names no
checksums, is refused and nothing is staged.

The binary is copied into a root-only staging directory and verified there,
through the descriptor that wrote it, as --release-target in the TUF metadata
in --release-metadata (threshold, timestamp expiry, snapshot rollback, length
and hashes); only then is it linked into place. Every directory on the way is
opened without following symlinks and must be root's; a symlink or a
directory another account owns or may write is refused. Nothing stages
automatically: a validator's operator runs this for every chain upgrade.

The chain unit 'orama global install' writes runs cosmovisor, which reads this
layout; install places the first oramad here as the genesis binary.`,
	Args: cobra.NoArgs,
	RunE: runStageOramad,
}

func init() {
	f := stageOramadCmd.Flags()
	f.StringVar(&stageFlags.binary, "binary", "", "The oramad binary to stage [required]")
	f.StringVar(&stageFlags.metadata, "release-metadata", "", "Directory holding timestamp.json, snapshot.json and targets.json [required]")
	f.StringVar(&stageFlags.target, "release-target", "", "Name the binary has in the release targets metadata [required]")
	f.StringVar(&stageFlags.verifier, "verifier", "", "The release's orama-orchard-verifier, staged beside oramad [required]")
	f.StringVar(&stageFlags.verifierTarget, "verifier-target", "", "Name the verifier has in the release targets metadata [required]")
	f.StringVar(&stageFlags.upgrade, "upgrade", "", "Upgrade plan name to stage for")
	f.StringVar(&stageFlags.planInfo, "plan-info", "", "File with the upgrade plan's info JSON, instead of asking the chain (with --upgrade)")
	f.BoolVar(&stageFlags.genesis, "genesis", false, "Stage the genesis binary instead of an upgrade")
	f.StringVar(&stageFlags.home, "home", constants.ChainHome, "cosmovisor DAEMON_HOME")
	MaintCmd.AddCommand(stageOramadCmd)
}

func runStageOramad(cmd *cobra.Command, _ []string) error {
	if stageFlags.binary == "" || stageFlags.metadata == "" || stageFlags.target == "" {
		return clierr.Usage("--binary, --release-metadata and --release-target are required")
	}
	if stageFlags.verifier == "" || stageFlags.verifierTarget == "" {
		return clierr.Usage("--verifier and --verifier-target are required: oramad does not start without the shielded verifier of its own release")
	}
	if stageFlags.genesis == (stageFlags.upgrade != "") {
		return clierr.Usage("give exactly one of --upgrade <name> and --genesis")
	}
	if stageFlags.planInfo != "" && stageFlags.upgrade == "" {
		return clierr.Usage("--plan-info goes with --upgrade")
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
	if err := checkVerifierPinned(stageFlags.binary, stageFlags.verifier); err != nil {
		return clierr.Failure("%v", err)
	}
	verify := verifyAsTarget(stageFlags.target)
	verifierVerify := verifyAsTarget(stageFlags.verifierTarget)
	if stageFlags.upgrade != "" {
		digests, err := planDigests(cmd.Context(), stageFlags.upgrade, stageFlags.planInfo)
		if err != nil {
			return err
		}
		verify, verifierVerify = plannedVerifies(stageFlags.upgrade, digests, verify, verifierVerify)
	}
	verifier := cosmovisor.Companion{Name: constants.ChainVerifierBinary, Src: stageFlags.verifier, Verify: verifierVerify}
	var dst string
	if stageFlags.genesis {
		dst, err = layout.StageGenesis(stageFlags.binary, verify, verifier)
	} else {
		dst, err = layout.StageUpgrade(stageFlags.upgrade, stageFlags.binary, verify, verifier)
	}
	if errors.Is(err, planinfo.ErrMismatch) {
		return clierr.Conflict("%v", err)
	}
	if err != nil {
		return clierr.Failure("%v", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "staged %s\n", dst)
	return nil
}

// verifyAsTarget checks a staged file, through its descriptor, as the named
// target of the release metadata under the adopted release root.
func verifyAsTarget(target string) cosmovisor.Verify {
	return func(f *os.File) error {
		_, err := releaseverify.CheckFile(releaseverify.FileCheck{
			RootPath:    releaseverify.RootPath,
			SeenPath:    releaseverify.SeenPath,
			MetadataDir: stageFlags.metadata,
			Target:      target,
			File:        f,
			Now:         time.Now(),
		})
		return err
	}
}

// checkVerifierPinned refuses an oramad and a verifier that are not of one
// release, before either is staged: a halted chain at the upgrade height is the
// price of finding out later.
func checkVerifierPinned(binary, verifier string) error {
	oramad, err := os.ReadFile(binary)
	if err != nil {
		return fmt.Errorf("read %s: %w", binary, err)
	}
	f, err := os.Open(verifier)
	if err != nil {
		return fmt.Errorf("read %s: %w", verifier, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("hash %s: %w", verifier, err)
	}
	return install.CheckOramadPinsVerifier(oramad, hex.EncodeToString(h.Sum(nil)))
}

// planInfoLimit bounds a --plan-info file: the chain bounds the info it carries far below it.
const planInfoLimit = 64 << 10

// planDigests is the checksums the plan name's info names for this platform.
func planDigests(ctx context.Context, name, infoFile string) (planinfo.Digests, error) {
	plan, err := stagePlan(ctx, name, infoFile)
	if err != nil {
		return planinfo.Digests{}, err
	}
	digests, err := planinfo.Parse(plan.Name, plan.Info, runtime.GOOS+"/"+runtime.GOARCH)
	if err != nil {
		return planinfo.Digests{}, clierr.Conflict("%v, so nothing is staged for it", err)
	}
	return digests, nil
}

// plannedVerifies adds to the verification of the oramad and of the verifier the check that the
// staged copy has the sha256 the plan names. The digest is of the file staging copied and linked
// into place, read through the descriptor it wrote, never of the path the operator gave: a file
// swapped after the plan was read is the file that is checked.
func plannedVerifies(plan string, d planinfo.Digests, oramad, verifier cosmovisor.Verify) (cosmovisor.Verify, cosmovisor.Verify) {
	return withDigest(oramad, func(sum string) error { return d.CheckOramad(plan, sum) }),
		withDigest(verifier, func(sum string) error { return d.CheckVerifier(plan, sum) })
}

// withDigest is verify followed by check on the sha256 of the whole staged file.
func withDigest(verify cosmovisor.Verify, check func(sum string) error) cosmovisor.Verify {
	return func(f *os.File) error {
		if err := verify(f); err != nil {
			return err
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return fmt.Errorf("rewind %s: %w", f.Name(), err)
		}
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			return fmt.Errorf("hash %s: %w", f.Name(), err)
		}
		return check(hex.EncodeToString(h.Sum(nil)))
	}
}

// stagePlan is the plan name's info: the file's content when infoFile is given, otherwise the
// chain's scheduled plan, which has to be name.
func stagePlan(ctx context.Context, name, infoFile string) (globalnode.UpgradePlan, error) {
	if infoFile != "" {
		data, err := os.ReadFile(infoFile)
		if err != nil {
			return globalnode.UpgradePlan{}, clierr.Failure("read --plan-info: %v", err)
		}
		if len(data) > planInfoLimit {
			return globalnode.UpgradePlan{}, clierr.Usage("--plan-info %s is larger than %d bytes", infoFile, planInfoLimit)
		}
		return globalnode.UpgradePlan{Name: name, Info: strings.TrimSpace(string(data))}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, planQueryBudget)
	defer cancel()
	base := chainRESTBase(globalnode.DefaultLifecycle(io.Discard).UnitDir)
	plan, err := globalnode.CurrentUpgradePlan(ctx, base, &http.Client{Timeout: planQueryBudget})
	if err != nil {
		return globalnode.UpgradePlan{}, clierr.Failure("the checksums to hold %s to are the scheduled plan's, and %v (--plan-info <file> stages from the plan's info JSON instead)", name, err)
	}
	if plan.Name != name {
		return globalnode.UpgradePlan{}, clierr.Conflict("the chain's scheduled upgrade is %q, not %q: only a scheduled plan's binaries can be staged (or give --plan-info)", plan.Name, name)
	}
	return plan, nil
}

// sha256File is the lowercase hex sha256 of the file at path.
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
