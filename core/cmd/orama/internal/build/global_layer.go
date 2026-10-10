package build

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/constants"
)

// The global layer is the chain node and its companions that `orama global
// install` puts on a machine: oramad with the orchard verifier library linked
// and the out-of-process verifier it pins, orama-global, the cosmovisor
// release tarball, and Kubo (the archive's ipfs, already built in). They ride
// in the archive's bin/ so one signed, hash-listed archive carries everything
// a node installs, and the installer checks each file against the manifest.
//
// The chain is built by its own module's Makefile (chain/Makefile), which
// cross-compiles for linux/amd64 only; there is no arm64 chain build.
const (
	chainDirName = "chain"
	// chainBuildDir is where the chain Makefile leaves its binaries.
	chainBuildDir = "build"
	// The Makefile's output names (BINARY-linux-amd64-full, and so on).
	builtOramad          = "oramad-linux-amd64-full"
	builtOrchardVerifier = constants.ChainVerifierBinary + "-linux-amd64"
	builtOramaGlobal     = "orama-global-linux-amd64"
	// globalLayerArch is the one architecture the chain builds for.
	globalLayerArch = "amd64"
	// globalLayerRustTarget is the musl target the static chain build links.
	globalLayerRustTarget = "x86_64-unknown-linux-musl"
	// skipGlobalFlag is the flag that leaves the layer out.
	skipGlobalFlag = "--skip-global-layer"
)

// oramaGlobalBinary is orama-global's name in the archive and in the installer's
// staged directory.
const oramaGlobalBinary = "orama-global"

// toolchain is how the build looks for and runs the programs the chain build
// needs; a test replaces it.
type toolchain struct {
	lookPath func(name string) (string, error)
	output   func(name string, args ...string) ([]byte, error)
}

func hostToolchain() toolchain {
	return toolchain{
		lookPath: exec.LookPath,
		output:   func(name string, args ...string) ([]byte, error) { return exec.Command(name, args...).Output() },
	}
}

// planGlobalLayer decides whether this build carries the global layer and, if
// it does, checks the toolchain it needs before anything is compiled. A build
// that cannot carry it fails here, saying what to install, rather than ending
// without the files a node's install will look for.
func (b *Builder) planGlobalLayer() error {
	if b.flags.SkipGlobalLayer {
		return nil
	}
	if b.flags.Arch != globalLayerArch {
		return clierr.Usage("the chain builds for linux/%s only, so a linux/%s archive cannot carry the global layer; pass %s for a cluster-only archive",
			globalLayerArch, b.flags.Arch, skipGlobalFlag)
	}
	if err := checkGlobalToolchain(hostToolchain()); err != nil {
		return err
	}
	b.globalLayer = true
	return nil
}

// checkGlobalToolchain names every program the chain build needs that is
// missing. zig is checked already (resolveZig), since the vault needs it too.
func checkGlobalToolchain(tc toolchain) error {
	var missing []string
	for _, name := range []string{"make", "cargo", "rustup", "rsync"} {
		if _, err := tc.lookPath(name); err != nil {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("the global layer (oramad, its verifier, orama-global) needs %s on PATH: install rustup (https://rustup.rs) and make, "+
			"or pass %s for an archive without it", strings.Join(missing, ", "), skipGlobalFlag)
	}
	out, err := tc.output("rustup", "target", "list", "--installed")
	if err != nil {
		return fmt.Errorf("list the installed rust targets with rustup: %w", err)
	}
	if !strings.Contains(string(out), globalLayerRustTarget) {
		return fmt.Errorf("the chain is built static for %s and rustup does not have that target: run `rustup target add %s`, "+
			"or pass %s for an archive without the global layer", globalLayerRustTarget, globalLayerRustTarget, skipGlobalFlag)
	}
	return nil
}

// buildGlobalLayer builds the chain's binaries, downloads the pinned
// cosmovisor release and puts the layer's files into the archive's bin/.
func (b *Builder) buildGlobalLayer() error {
	fmt.Println("Global layer: oramad, the orchard verifier, orama-global, cosmovisor...")
	chainDir := filepath.Join(b.projectDir, "..", chainDirName)
	if _, err := os.Stat(filepath.Join(chainDir, "Makefile")); err != nil {
		return fmt.Errorf("the chain module is missing at %s (expected the repository's chain/ directory): %w", chainDir, err)
	}
	cmd := exec.Command("make", "-C", chainDir, "build-linux-amd64-full", "build-linux-amd64-global", "ORAMA_ZIG="+b.zig)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("build the chain (make -C %s build-linux-amd64-full build-linux-amd64-global): %w; "+
			"it needs zig, make, rsync and rustup with the %s target, and cargo", chainDir, err, globalLayerRustTarget)
	}
	if err := stageGlobalFiles(filepath.Join(chainDir, chainBuildDir), b.binDir); err != nil {
		return err
	}
	tarball := constants.CosmovisorTarball(globalLayerArch)
	if err := fetchPinned(constants.CosmovisorTarballURL(globalLayerArch), filepath.Join(b.binDir, tarball), tarball, globalLayerArch, constants.CosmovisorTarballSHA256); err != nil {
		return fmt.Errorf("download cosmovisor %s: %w", constants.CosmovisorVersion, err)
	}
	fmt.Println("  ✓ oramad, orama-orchard-verifier, orama-global, cosmovisor")
	return nil
}

// stageGlobalFiles copies the chain build's outputs into binDir under the names
// the installer looks for, after checking that they belong together: the
// verifier's sidecar digest is the verifier's, and oramad has that digest
// linked in (it runs no other file, so a mismatched pair is an oramad that
// cannot start).
func stageGlobalFiles(buildDir, binDir string) error {
	verifier := filepath.Join(buildDir, builtOrchardVerifier)
	sum, err := sha256File(verifier)
	if err != nil {
		return fmt.Errorf("hash the built verifier: %w", err)
	}
	sidecar, err := os.ReadFile(verifier + ".sha256")
	if err != nil {
		return fmt.Errorf("read the verifier's digest file: %w", err)
	}
	if got := strings.TrimSpace(string(sidecar)); got != sum {
		return fmt.Errorf("%s.sha256 says %s but the verifier is %s: the chain build is inconsistent", builtOrchardVerifier, got, sum)
	}
	oramad := filepath.Join(buildDir, builtOramad)
	linked, err := fileContains(oramad, sum)
	if err != nil {
		return fmt.Errorf("read the built oramad: %w", err)
	}
	if !linked {
		return fmt.Errorf("the built oramad does not pin the verifier's digest %s; it would refuse to start with this verifier. Rebuild the chain (make -C chain build-linux-amd64-full)", sum)
	}
	for src, dst := range map[string]string{
		oramad:   constants.ChainDaemonName,
		verifier: constants.ChainVerifierBinary,
		filepath.Join(buildDir, builtOramaGlobal): oramaGlobalBinary,
	} {
		if err := copyFile(src, filepath.Join(binDir, dst)); err != nil {
			return fmt.Errorf("add %s to the archive: %w", dst, err)
		}
	}
	return os.WriteFile(filepath.Join(binDir, constants.ChainVerifierSHA256File), []byte(sum+"\n"), 0o644)
}

// fileContains reports whether the file holds needle.
func fileContains(path, needle string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	return bytes.Contains(data, []byte(needle)), nil
}
