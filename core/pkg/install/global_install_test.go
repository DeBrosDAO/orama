package install

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/rootfs"
)

// fakeGlobalNode is the commands a global install runs, answered from memory.
type fakeGlobalNode struct {
	*fakeAccounts
	ufwActive bool
	chainHome string
	ufwFail   string
	sshdPort  string
	// kuboVersion is what the staged ipfs --version prints.
	kuboVersion string
}

func (f *fakeGlobalNode) run(name string, args ...string) ([]byte, error) {
	switch name {
	case "getent", "useradd", "id", "usermod":
		return f.fakeAccounts.run(name, args...)
	}
	f.calls = append(f.calls, append([]string{name}, args...))
	switch name {
	case "sshd":
		return []byte("port " + f.sshdPort + "\nlistenaddress 0.0.0.0:" + f.sshdPort + "\n"), nil
	case "groupadd":
		f.groups[args[len(args)-1]] = true
	case "ufw":
		if strings.Join(args, " ") == f.ufwFail {
			return []byte("ufw: refused"), exitErr(1)
		}
		if args[0] == "status" {
			if f.ufwActive {
				return []byte("Status: active\n"), nil
			}
			return []byte("Status: inactive\n"), nil
		}
		if args[0] == "--force" {
			f.ufwActive = true
		}
	case "runuser":
		return f.runuser(args)
	}
	return nil, nil
}

// runuser answers the commands run as a service account: oramad init, and the
// staged ipfs's --version and init.
func (f *fakeGlobalNode) runuser(args []string) ([]byte, error) {
	joined := strings.Join(args, " ")
	switch {
	case strings.HasSuffix(joined, "ipfs --version"):
		return []byte(f.kuboVersion), nil
	case strings.Contains(joined, "ipfs init"):
		for _, a := range args {
			if repo, ok := strings.CutPrefix(a, "--repo-dir="); ok {
				return nil, os.WriteFile(filepath.Join(repo, "config"), []byte(`{"Identity":{"PeerID":"12D3KooWtest"}}`), 0o600)
			}
		}
		return nil, fmt.Errorf("ipfs init without --repo-dir")
	}
	if err := os.MkdirAll(filepath.Join(f.chainHome, "config"), 0o700); err != nil {
		return nil, err
	}
	return nil, os.WriteFile(filepath.Join(f.chainHome, "config", "genesis.json"), []byte(`{"chain_id":"init"}`), 0o600)
}

// named is every call whose command is name.
func (f *fakeGlobalNode) named(name string) []string {
	var out []string
	for _, c := range f.calls {
		if c[0] == name {
			out = append(out, strings.Join(c[1:], " "))
		}
	}
	return out
}

type chownCall struct {
	path     string
	uid, gid int
}

type stageCall struct{ src, sum string }

type globalFixture struct {
	node   *fakeGlobalNode
	host   GlobalHost
	staged string
	chowns []chownCall
	stages []stageCall
	// tarball is the staged cosmovisor release, whose SHA-256 the host pins.
	tarball []byte
}

// cosmovisorTarball is a release tarball holding a LICENSE and the cosmovisor file.
func cosmovisorTarball(t *testing.T, binary string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range []struct{ name, body string }{{"LICENSE", "license text"}, {"cosmovisor", binary}} {
		if err := tw.WriteHeader(&tar.Header{Name: e.name, Mode: 0o755, Size: int64(len(e.body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func newGlobalFixture(t *testing.T) *globalFixture {
	t.Helper()
	tmp := t.TempDir()
	f := &globalFixture{staged: filepath.Join(tmp, "staged")}
	for _, dir := range []string{f.staged, filepath.Join(tmp, "usrlib"), filepath.Join(tmp, "units"), filepath.Join(tmp, "varlib")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"oramad", "orama", "orama-global", "ipfs"} {
		if err := os.WriteFile(filepath.Join(f.staged, name), []byte("binary "+name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	f.tarball = cosmovisorTarball(t, "binary cosmovisor")
	if err := os.WriteFile(filepath.Join(f.staged, constants.CosmovisorTarball("amd64")), f.tarball, 0o600); err != nil {
		t.Fatal(err)
	}
	pin := sha256.Sum256(f.tarball)
	chainHome := filepath.Join(tmp, "varlib", "orama-global", "chain")
	// The chain home exists with a genesis, as after --init-chain or a restore.
	if err := os.MkdirAll(filepath.Join(chainHome, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chainHome, "config", "genesis.json"), []byte(`{"chain_id":"existing"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	f.node = &fakeGlobalNode{fakeAccounts: newFakeAccounts(), ufwActive: true, chainHome: chainHome, sshdPort: "22", kuboVersion: "ipfs version 0.43.1\n"}
	f.host = GlobalHost{
		Run:            f.node.run,
		BinRoot:        rootfs.At(filepath.Join(tmp, "usrlib")),
		BinDir:         filepath.Join(tmp, "usrlib", "orama-global", "bin"),
		UnitRoot:       rootfs.At(filepath.Join(tmp, "units")),
		UnitDir:        filepath.Join(tmp, "units"),
		StateRoot:      rootfs.At(filepath.Join(tmp, "varlib")),
		StateDir:       filepath.Join(tmp, "varlib", "orama-global"),
		ChainHome:      chainHome,
		Lookup:         func(string) (int, int, error) { return 990, 991, nil },
		LookupGroup:    func(string) (int, error) { return 995, nil },
		Arch:           "amd64",
		CosmovisorPins: map[string]string{"amd64": hex.EncodeToString(pin[:])},
		StageGenesis: func(src, sum string) (string, error) {
			f.stages = append(f.stages, stageCall{src, sum})
			return filepath.Join(chainHome, "cosmovisor", "genesis", "bin", "oramad"), nil
		},
		Chown: func(_ rootfs.Root, path string, uid, gid int) error {
			f.chowns = append(f.chowns, chownCall{path, uid, gid})
			return nil
		},
		Logf: func(string, ...any) {},
	}
	return f
}

func (f *globalFixture) options(services ...GlobalService) GlobalInstallOptions {
	return GlobalInstallOptions{Services: services, StagedDir: f.staged, SSHPort: 22, PublicStorageBytes: 500_000_000_000}
}

// freshHome removes the chain home the fixture starts with, for --init-chain.
func (f *globalFixture) freshHome(t *testing.T) {
	t.Helper()
	if err := os.RemoveAll(f.host.ChainHome); err != nil {
		t.Fatal(err)
	}
}

func TestInstallGlobal_chainAndProviderPlan(t *testing.T) {
	f := newGlobalFixture(t)
	if err := InstallGlobal(f.options(GlobalServiceChain, GlobalServiceIPFS, GlobalServiceProvider), f.host); err != nil {
		t.Fatal(err)
	}
	for _, user := range []string{constants.ChainUser, "orama-provider"} {
		if !f.node.users[user] {
			t.Errorf("account %s was not created", user)
		}
	}
	if got := f.node.named("groupadd"); !slices.Equal(got, []string{"--system orama-ipfs-pub-rpc"}) {
		t.Errorf("groupadd calls = %v", got)
	}
	for _, name := range []string{"oramad", "orama", "orama-global", "ipfs", "cosmovisor"} {
		path := filepath.Join(f.host.BinDir, name)
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o755 || !info.Mode().IsRegular() {
			t.Errorf("%s mode %v, want a regular 0755 file", name, info.Mode())
		}
		if !slices.Contains(f.chowns, chownCall{path, 0, 0}) {
			t.Errorf("%s was not made root's", name)
		}
	}
	if !slices.Contains(f.chowns, chownCall{f.host.BinDir, 0, 0}) {
		t.Error("the bin directory was not made root's")
	}
	chainUnit, err := os.ReadFile(filepath.Join(f.host.UnitDir, constants.ChainServiceUnit))
	if err != nil {
		t.Fatal(err)
	}
	if string(chainUnit) != RenderGlobalChainUnit("") {
		t.Error("the chain unit is not the cosmovisor unit")
	}
	if _, err := os.Stat(filepath.Join(f.host.UnitDir, constants.GlobalArchiverUnit)); !os.IsNotExist(err) {
		t.Error("an archiver unit was written though archiver was not asked for")
	}
	wantSystemctl := []string{"daemon-reload", "enable " + constants.ChainServiceUnit, "enable " + constants.GlobalIPFSUnit,
		"enable " + globalIPFSGCTimer, "enable " + constants.GlobalProviderUnit}
	if got := f.node.named("systemctl"); !slices.Equal(got, wantSystemctl) {
		t.Errorf("systemctl calls = %v, want %v", got, wantSystemctl)
	}
	wantUFW := []string{"status", "allow 31000/tcp comment orama-global", "allow 31000/udp comment orama-global",
		"allow 31010/tcp comment orama-global", "allow 31010/udp comment orama-global", "allow 31013/tcp comment orama-global"}
	if got := f.node.named("ufw"); !slices.Equal(got, wantUFW) {
		t.Errorf("ufw calls = %v, want %v", got, wantUFW)
	}
	for _, call := range f.node.named("runuser") {
		if strings.Contains(call, "oramad") {
			t.Errorf("the chain home was initialised without --init-chain: %s", call)
		}
	}
}

func TestInstallGlobal_secondRunIsIdempotent(t *testing.T) {
	f := newGlobalFixture(t)
	opts := f.options(GlobalServiceChain, GlobalServiceArchiver)
	if err := InstallGlobal(opts, f.host); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(filepath.Join(f.host.UnitDir, constants.GlobalArchiverUnit))
	f.node.calls = nil
	if err := InstallGlobal(opts, f.host); err != nil {
		t.Fatalf("second install: %v", err)
	}
	if changes := f.node.changes(); len(changes) != 0 {
		t.Errorf("second install changed accounts: %v", changes)
	}
	second, _ := os.ReadFile(filepath.Join(f.host.UnitDir, constants.GlobalArchiverUnit))
	if string(first) != string(second) || len(second) == 0 {
		t.Error("the archiver unit changed between two identical installs")
	}
}

func TestInstallGlobal_inactiveFirewallRefusedBeforeAnyChange(t *testing.T) {
	f := newGlobalFixture(t)
	f.node.ufwActive = false
	err := InstallGlobal(f.options(GlobalServiceChain), f.host)
	if err == nil || !strings.Contains(err.Error(), "--enable-firewall") {
		t.Fatalf("err = %v, want a refusal naming --enable-firewall", err)
	}
	if len(f.node.changes()) != 0 || f.node.named("systemctl") != nil {
		t.Errorf("the host changed before the refusal: %v", f.node.calls)
	}
	if _, err := os.Stat(f.host.BinDir); !os.IsNotExist(err) {
		t.Error("binaries were installed before the refusal")
	}
}

func TestInstallGlobal_enableFirewallAllowsSSHBeforeEnabling(t *testing.T) {
	f := newGlobalFixture(t)
	f.node.ufwActive = false
	f.node.sshdPort = "2222"
	opts := f.options(GlobalServiceChain)
	opts.EnableFirewall, opts.SSHPort = true, 2222
	if err := InstallGlobal(opts, f.host); err != nil {
		t.Fatal(err)
	}
	got := f.node.named("ufw")
	want := []string{"status", "default deny incoming", "default allow outgoing",
		"allow 2222/tcp comment orama-global", "--force enable",
		"allow 31000/tcp comment orama-global", "allow 31000/udp comment orama-global"}
	if !slices.Equal(got, want) {
		t.Fatalf("ufw calls = %v, want %v", got, want)
	}
}

func TestInstallGlobal_failedFirewallRuleIsAnError(t *testing.T) {
	f := newGlobalFixture(t)
	f.node.ufwFail = "allow 31000/udp comment orama-global"
	err := InstallGlobal(f.options(GlobalServiceChain), f.host)
	if err == nil || !strings.Contains(err.Error(), "31000/udp") {
		t.Fatalf("err = %v, want the failing rule named", err)
	}
}

func TestInstallGlobal_symlinkedStagedBinaryRefused(t *testing.T) {
	f := newGlobalFixture(t)
	target := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.WriteFile(target, []byte("not a release"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(f.staged, "oramad")
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	err := InstallGlobal(f.options(GlobalServiceChain), f.host)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("err = %v, want a symlink refusal", err)
	}
	if _, err := os.Stat(filepath.Join(f.host.BinDir, "oramad")); !os.IsNotExist(err) {
		t.Error("the symlink's target was installed")
	}
}

func TestInstallGlobal_missingStagedBinaryNamesIt(t *testing.T) {
	f := newGlobalFixture(t)
	if err := os.Remove(filepath.Join(f.staged, "orama-global")); err != nil {
		t.Fatal(err)
	}
	err := InstallGlobal(f.options(GlobalServiceChain, GlobalServiceRepair), f.host)
	if err == nil || !strings.Contains(err.Error(), "orama-global") {
		t.Fatalf("err = %v, want the missing binary named", err)
	}
}

func TestInstallGlobal_invalidOptionsRefused(t *testing.T) {
	f := newGlobalFixture(t)
	cases := map[string]GlobalInstallOptions{
		"no services":                   {StagedDir: f.staged, SSHPort: 22},
		"no staged dir":                 {Services: []GlobalService{GlobalServiceChain}, SSHPort: 22},
		"zero ssh port":                 {Services: []GlobalService{GlobalServiceChain}, StagedDir: f.staged},
		"peer injection":                {Services: []GlobalService{GlobalServiceChain}, StagedDir: f.staged, SSHPort: 22, PersistentPeers: "x@y:1\nExecStartPre=/bin/sh"},
		"ipfs without a storage budget": {Services: []GlobalService{GlobalServiceChain, GlobalServiceIPFS}, StagedDir: f.staged, SSHPort: 22},
		"init without genesis": {Services: []GlobalService{GlobalServiceChain}, StagedDir: f.staged, SSHPort: 22,
			InitChain: &ChainInit{ChainID: "orama-test-1", Moniker: "a"}},
	}
	for name, opts := range cases {
		if err := InstallGlobal(opts, f.host); err == nil {
			t.Errorf("%s: install accepted it", name)
		}
	}
	if len(f.node.calls) != 0 {
		t.Errorf("invalid options ran commands: %v", f.node.calls)
	}
}

func TestInstallGlobal_enableFirewallRefusesAnSSHPortSSHDoesNotUse(t *testing.T) {
	f := newGlobalFixture(t)
	f.node.ufwActive = false
	opts := f.options(GlobalServiceChain)
	opts.EnableFirewall, opts.SSHPort = true, 2222
	err := InstallGlobal(opts, f.host)
	if err == nil || !strings.Contains(err.Error(), "sshd listens on port 22") {
		t.Fatalf("err = %v, want the sshd port mismatch", err)
	}
	if got := f.node.named("ufw"); !slices.Equal(got, []string{"status"}) {
		t.Fatalf("ufw changed before the refusal: %v", got)
	}
	if len(f.node.changes()) != 0 {
		t.Fatal("accounts changed before the refusal")
	}
}
