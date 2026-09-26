package installers

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

// TorInstaller installs the Tor client and writes its torrc.
type TorInstaller struct {
	*BaseInstaller
	run      commandRunner
	fetchKey func() ([]byte, error)
	// root prefixes every file the installer reads or writes; "/" on a node.
	root string
}

// NewTorInstaller creates a Tor client installer.
func NewTorInstaller(arch string, logWriter io.Writer) *TorInstaller {
	return &TorInstaller{
		BaseInstaller: NewBaseInstaller(arch, logWriter),
		run:           execRunner,
		fetchKey:      fetchTorArchiveKey,
		root:          "/",
	}
}

func (ti *TorInstaller) path(p string) string { return filepath.Join(ti.root, p) }

// Install installs Tor from deb.torproject.org, or upgrades it to the
// repository's current release: it masks the distro units, adds the repository
// if this installer's source and keyring are not in place for the OS release,
// then runs apt-get update and apt-get install. It always needs the network.
func (ti *TorInstaller) Install() error {
	if err := ti.maskDistroUnits(); err != nil {
		return err
	}
	suite, err := ti.suite()
	if err != nil {
		return err
	}
	repoCurrent, err := ti.repositoryCurrent(suite)
	if err != nil {
		return err
	}
	if !repoCurrent {
		if err := ti.addRepository(suite); err != nil {
			return err
		}
	}
	fmt.Fprintf(ti.logWriter, "  Installing/upgrading Tor client (deb.torproject.org, suite %s)...\n", suite)
	if err := aptGet(ti.run, "update"); err != nil {
		return fmt.Errorf("apt-get update with %s: %w", TorAptSourcePath, err)
	}
	if err := aptGet(ti.run, append([]string{"install", "-y"}, TorAptPackages...)...); err != nil {
		return fmt.Errorf("install %s from deb.torproject.org: %w", strings.Join(TorAptPackages, ", "), err)
	}
	fmt.Fprintf(ti.logWriter, "  ✓ Tor client installed and current\n")
	return nil
}

// EnsureInstalled runs Install only when Tor is missing or its repository is
// not this installer's for the OS release; otherwise it only re-masks the
// distro units and touches no network. It is for the post-swap half of an
// upgrade, which runs with the node's services stopped: Tor was already
// upgraded, while they were still serving, by the Install in the pre-stop half.
func (ti *TorInstaller) EnsureInstalled() error {
	if err := ti.maskDistroUnits(); err != nil {
		return err
	}
	suite, err := ti.suite()
	if err != nil {
		return err
	}
	installed, err := ti.packagesInstalled()
	if err != nil {
		return err
	}
	repoCurrent, err := ti.repositoryCurrent(suite)
	if err != nil {
		return err
	}
	if installed && repoCurrent {
		fmt.Fprintf(ti.logWriter, "  ✓ Tor client already installed from deb.torproject.org (suite %s)\n", suite)
		return nil
	}
	return ti.Install()
}

// addRepository fetches the Tor archive key, keeps only the pinned key, and
// writes the keyring and the apt source for suite.
func (ti *TorInstaller) addRepository(suite string) error {
	if err := aptGet(ti.run, "update"); err != nil {
		return fmt.Errorf("apt-get update before installing gnupg: %w", err)
	}
	if err := aptGet(ti.run, "install", "-y", "gnupg"); err != nil {
		return fmt.Errorf("install gnupg (needed to verify the Tor archive key): %w", err)
	}
	armored, err := ti.fetchKey()
	if err != nil {
		return err
	}
	if err := ti.installVerifiedKeyring(armored); err != nil {
		return err
	}
	if err := os.WriteFile(ti.path(TorAptSourcePath), []byte(TorAptSource(suite)), 0644); err != nil {
		return fmt.Errorf("write %s: %w", TorAptSourcePath, err)
	}
	fmt.Fprintf(ti.logWriter, "    ✓ Tor Project repository added (suite %s)\n", suite)
	return nil
}

// Configure writes the Orama torrc.
func (ti *TorInstaller) Configure() error {
	path := ti.path(constants.TorConfigPath)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(GenerateTorrc()), 0644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	fmt.Fprintf(ti.logWriter, "  ✓ Tor client configured (%s, SOCKS %s)\n", constants.TorConfigPath, constants.TorSOCKSAddr())
	return nil
}

// packagesInstalled reports whether every Tor package is fully installed.
func (ti *TorInstaller) packagesInstalled() (bool, error) {
	for _, pkg := range TorAptPackages {
		ok, err := ti.packageInstalled(pkg)
		if err != nil || !ok {
			return false, err
		}
	}
	return true, nil
}

func (ti *TorInstaller) packageInstalled(pkg string) (bool, error) {
	out, err := ti.run("dpkg-query", "-W", "-f=${db:Status-Status}", pkg)
	if err != nil {
		if strings.Contains(out, "no packages found") {
			return false, nil
		}
		return false, fmt.Errorf("query the dpkg status of %s: %w (%s)", pkg, err, strings.TrimSpace(out))
	}
	return strings.TrimSpace(out) == "installed", nil
}

// repositoryCurrent reports whether the apt source on disk is exactly the one
// this installer writes for suite, and its keyring is the one the
// deb.torproject.org-keyring package maintains.
func (ti *TorInstaller) repositoryCurrent(suite string) (bool, error) {
	if ok, err := ti.packageInstalled(TorKeyringPackage); err != nil || !ok {
		return false, err
	}
	source, err := os.ReadFile(ti.path(TorAptSourcePath))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read %s: %w", TorAptSourcePath, err)
	}
	if string(source) != TorAptSource(suite) {
		return false, nil
	}
	if _, err := os.Stat(ti.path(TorKeyringPath)); os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("stat %s: %w", TorKeyringPath, err)
	}
	return true, nil
}

// maskDistroUnits masks tor.service and tor@default.service and stops them if
// an earlier install left them running. Masking a unit that is not installed
// yet is allowed, and is what keeps the package from starting it on install.
func (ti *TorInstaller) maskDistroUnits() error {
	if err := runChecked(ti.run, "systemctl", append([]string{"mask"}, TorDistroUnits...)...); err != nil {
		return fmt.Errorf("mask the distro Tor units %v: %w", TorDistroUnits, err)
	}
	for _, unit := range TorDistroUnits {
		out, err := ti.run("systemctl", "is-active", unit)
		if err != nil || strings.TrimSpace(out) != "active" {
			continue
		}
		if err := runChecked(ti.run, "systemctl", "stop", unit); err != nil {
			return fmt.Errorf("stop %s (it holds the Tor SOCKS port): %w", unit, err)
		}
	}
	return nil
}

func (ti *TorInstaller) suite() (string, error) {
	data, err := os.ReadFile(ti.path("/etc/os-release"))
	if err != nil {
		return "", fmt.Errorf("read /etc/os-release to pick the Tor apt suite: %w", err)
	}
	codename := ParseOSReleaseCodename(string(data))
	if codename == "" {
		return "", fmt.Errorf("/etc/os-release has no VERSION_CODENAME; cannot pick the Tor apt suite")
	}
	return TorSuiteFor(codename)
}

// ParseOSReleaseCodename returns VERSION_CODENAME from os-release content.
func ParseOSReleaseCodename(osRelease string) string {
	for _, line := range strings.Split(osRelease, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "VERSION_CODENAME="); ok {
			return strings.Trim(v, `"'`)
		}
	}
	return ""
}
