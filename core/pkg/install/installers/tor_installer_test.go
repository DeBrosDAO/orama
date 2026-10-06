package installers

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeTorHost answers the commands TorInstaller runs from a model of a node
// and records them in order.
type fakeTorHost struct {
	t             *testing.T
	root          string
	installed     map[string]bool // dpkg packages in "installed" state
	activeUnits   map[string]bool
	keyColons     string
	calls         []string
	sourceAtApt   []bool // whether tor.sources existed at each apt-get update
	keyDownloads  int
	failAptUpdate bool
}

func (f *fakeTorHost) run(name string, args ...string) (string, error) {
	call := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, call)
	switch {
	case name == "systemctl" && args[0] == "is-active":
		if f.activeUnits[args[1]] {
			return "active\n", nil
		}
		return "inactive\n", errors.New("exit status 3")
	case name == "systemctl":
		return "", nil
	case name == "dpkg-query":
		if f.installed[args[len(args)-1]] {
			return "installed", nil
		}
		return "dpkg-query: no packages found matching " + args[len(args)-1], errors.New("exit status 1")
	case name == "apt-get" && args[len(args)-1] == "update":
		_, err := os.Stat(filepath.Join(f.root, TorAptSourcePath))
		f.sourceAtApt = append(f.sourceAtApt, err == nil)
		if f.failAptUpdate {
			return "E: could not get lock", errors.New("exit status 100")
		}
		return "", nil
	case name == "apt-get":
		return "", nil
	case name == "gpg" && containsString(args, "--list-keys"):
		return f.keyColons, nil
	case name == "gpg" || name == "gpgconf":
		return "", nil
	}
	f.t.Fatalf("unexpected command: %s", call)
	return "", nil
}

func newFakeTorHost(t *testing.T, codename string) *fakeTorHost {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"etc/apt/sources.list.d", "usr/share/keyrings"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	osRelease := "ID=ubuntu\nVERSION_CODENAME=" + codename + "\n"
	if err := os.WriteFile(filepath.Join(root, "etc/os-release"), []byte(osRelease), 0o644); err != nil {
		t.Fatal(err)
	}
	return &fakeTorHost{t: t, root: root, installed: map[string]bool{}, activeUnits: map[string]bool{}, keyColons: torKeyColons}
}

func (f *fakeTorHost) installer() *TorInstaller {
	ti := NewTorInstaller("amd64", io.Discard)
	ti.run = f.run
	ti.root = f.root
	ti.fetchKey = func() ([]byte, error) {
		f.keyDownloads++
		return []byte("-----BEGIN PGP PUBLIC KEY BLOCK-----"), nil
	}
	return ti
}

func (f *fakeTorHost) indexContaining(sub string) int {
	for i, c := range f.calls {
		if strings.Contains(c, sub) {
			return i
		}
	}
	return -1
}

// currentNode makes host look like a node Tor was installed on earlier.
func (f *fakeTorHost) currentNode(suite string) {
	f.t.Helper()
	f.installed = map[string]bool{"tor": true, TorKeyringPackage: true}
	if err := os.WriteFile(filepath.Join(f.root, TorAptSourcePath), []byte(TorAptSource(suite)), 0o644); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.root, TorKeyringPath), []byte("k"), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fakeTorHost) indexOf(prefix string) int {
	for i, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return i
		}
	}
	return -1
}

const aptLock = "apt-get -o DPkg::Lock::Timeout=300 "

func TestTorInstaller_Install_freshNode(t *testing.T) {
	host := newFakeTorHost(t, "noble")
	if err := host.installer().Install(); err != nil {
		t.Fatalf("Install: %v", err)
	}
	mask := host.indexOf("systemctl mask tor.service tor@default.service")
	install := host.indexOf(aptLock + "install -y tor deb.torproject.org-keyring")
	export := host.indexContaining("--export -o")
	if mask != 0 {
		t.Errorf("the distro units must be masked first, calls: %v", host.calls)
	}
	if install < 0 || install < mask || install < export {
		t.Errorf("tor must be installed after masking and after the keyring is written, calls: %v", host.calls)
	}
	if host.keyDownloads != 1 {
		t.Errorf("archive key downloaded %d times, want 1", host.keyDownloads)
	}
	src, err := os.ReadFile(filepath.Join(host.root, TorAptSourcePath))
	if err != nil || string(src) != TorAptSource("noble") {
		t.Errorf("apt source = %q, %v; want the noble source", src, err)
	}
	// The second update is the one that must see the Tor repository.
	if len(host.sourceAtApt) != 2 || host.sourceAtApt[0] || !host.sourceAtApt[1] {
		t.Errorf("tor.sources present at apt-get update: %v, want [false true]", host.sourceAtApt)
	}
	if !strings.Contains(strings.Join(host.calls, "\n"), "--export -o "+filepath.Join(host.root, TorKeyringPath)+" "+TorArchiveKeyFingerprint) {
		t.Errorf("only the pinned key may be exported to the keyring, calls: %v", host.calls)
	}
}

// Install on a node that already has Tor upgrades it: apt runs, but the
// repository is not re-added and the key is not fetched again.
func TestTorInstaller_Install_currentNodeUpgradesTor(t *testing.T) {
	host := newFakeTorHost(t, "noble")
	host.currentNode("noble")
	if err := host.installer().Install(); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if host.keyDownloads != 0 {
		t.Errorf("a current repository must not be re-added, calls: %v", host.calls)
	}
	if host.indexOf(aptLock+"update") < 0 || host.indexOf(aptLock+"install -y tor deb.torproject.org-keyring") < 0 {
		t.Errorf("Install must upgrade tor through apt, calls: %v", host.calls)
	}
}

// The post-swap half of an upgrade runs with the node's services stopped;
// once Tor is installed from the Tor Project repository it must not touch the
// network there.
func TestTorInstaller_EnsureInstalled_currentNodeTouchesNoNetwork(t *testing.T) {
	host := newFakeTorHost(t, "noble")
	host.currentNode("noble")
	if err := host.installer().EnsureInstalled(); err != nil {
		t.Fatalf("EnsureInstalled: %v", err)
	}
	if host.keyDownloads != 0 || host.indexOf("apt-get") >= 0 {
		t.Errorf("an up-to-date node must not download or run apt, calls: %v", host.calls)
	}
	if host.indexOf("systemctl mask") != 0 {
		t.Errorf("the distro units must still be masked, calls: %v", host.calls)
	}
}

// The first upgrade after Anyone reaches EnsureInstalled with no Tor at all.
func TestTorInstaller_EnsureInstalled_missingTorInstallsIt(t *testing.T) {
	host := newFakeTorHost(t, "noble")
	if err := host.installer().EnsureInstalled(); err != nil {
		t.Fatalf("EnsureInstalled: %v", err)
	}
	if host.keyDownloads != 1 || host.indexOf(aptLock+"install -y tor deb.torproject.org-keyring") < 0 {
		t.Errorf("a node without Tor must get it, calls: %v", host.calls)
	}
}

// A source for another release (the OS was upgraded) is not current.
func TestTorInstaller_EnsureInstalled_staleSourceReinstalls(t *testing.T) {
	host := newFakeTorHost(t, "noble")
	host.currentNode("jammy")
	if err := host.installer().EnsureInstalled(); err != nil {
		t.Fatalf("EnsureInstalled: %v", err)
	}
	if host.keyDownloads != 1 {
		t.Error("a source for another suite must be rewritten")
	}
}

// Stopping the gpg daemons of the throwaway home must not be skipped.
func TestTorInstaller_Install_stopsTheTemporaryGPGDaemons(t *testing.T) {
	host := newFakeTorHost(t, "noble")
	if err := host.installer().Install(); err != nil {
		t.Fatalf("Install: %v", err)
	}
	kill := host.indexContaining("gpgconf --homedir")
	if kill < 0 || kill < host.indexContaining("--export -o") {
		t.Errorf("gpgconf --kill all must run after the export, calls: %v", host.calls)
	}
}

func TestTorInstaller_Install_wrongKeyIsRefused(t *testing.T) {
	host := newFakeTorHost(t, "noble")
	host.keyColons = strings.ReplaceAll(torKeyColons, TorArchiveKeyFingerprint, "0000000000000000000000000000000000000000")
	if err := host.installer().Install(); err == nil {
		t.Fatal("a key that is not the pinned one must fail the install")
	}
	if host.indexOf("gpg --homedir") >= 0 && strings.Contains(strings.Join(host.calls, "\n"), "--export") {
		t.Error("a refused key must never be exported to the keyring")
	}
	if host.indexOf(aptLock+"install -y tor") >= 0 {
		t.Error("tor must not be installed from an unverified repository")
	}
	if _, err := os.Stat(filepath.Join(host.root, TorAptSourcePath)); err == nil {
		t.Error("the apt source must not be written for an unverified key")
	}
}

func TestTorInstaller_Install_unpublishedCodenameFailsBeforeApt(t *testing.T) {
	host := newFakeTorHost(t, "plucky")
	err := host.installer().Install()
	if err == nil || !strings.Contains(err.Error(), "plucky") {
		t.Fatalf("err = %v, want one naming the codename", err)
	}
	if host.indexOf("apt-get") >= 0 || host.keyDownloads != 0 {
		t.Errorf("nothing may be fetched for an unsupported OS, calls: %v", host.calls)
	}
}

func TestTorInstaller_Install_stopsARunningDistroInstance(t *testing.T) {
	host := newFakeTorHost(t, "noble")
	host.activeUnits["tor@default.service"] = true
	if err := host.installer().Install(); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if host.indexOf("systemctl stop tor@default.service") < 0 {
		t.Errorf("a running tor@default holds 9050 and must be stopped, calls: %v", host.calls)
	}
	if host.indexOf("systemctl stop tor.service") >= 0 {
		t.Error("an inactive unit must not be stopped")
	}
}

func TestTorInstaller_Install_aptFailureIsReturned(t *testing.T) {
	host := newFakeTorHost(t, "noble")
	host.failAptUpdate = true
	err := host.installer().Install()
	if err == nil || !strings.Contains(err.Error(), "could not get lock") {
		t.Fatalf("err = %v, want the apt failure with its output", err)
	}
}

func TestTorInstaller_Configure_writesTorrc(t *testing.T) {
	host := newFakeTorHost(t, "noble")
	if err := host.installer().Configure(); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(host.root, "etc/orama/tor/torrc"))
	if err != nil || string(got) != GenerateTorrc() {
		t.Errorf("torrc = %q, %v", got, err)
	}
}
