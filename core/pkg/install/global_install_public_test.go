package install

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

func TestCosmovisorPin_isTheOfficialReleaseDigests(t *testing.T) {
	hexSum := regexp.MustCompile(`^[0-9a-f]{64}$`)
	for _, arch := range []string{"amd64", "arm64"} {
		if !hexSum.MatchString(constants.CosmovisorTarballSHA256[arch]) {
			t.Errorf("%s pin %q is not a SHA-256", arch, constants.CosmovisorTarballSHA256[arch])
		}
	}
	if constants.CosmovisorTarballSHA256["amd64"] == constants.CosmovisorTarballSHA256["arm64"] {
		t.Error("both architectures pin the same digest")
	}
	if got := constants.CosmovisorTarball("arm64"); got != "cosmovisor-v1.7.3-linux-arm64.tar.gz" {
		t.Errorf("tarball name %q", got)
	}
	if h := DefaultGlobalHost(func(string, ...any) {}); h.CosmovisorPins["amd64"] != constants.CosmovisorTarballSHA256["amd64"] {
		t.Error("the real host does not use the pinned digests")
	}
}

func TestInstallGlobal_cosmovisorComesFromTheVerifiedTarballOnly(t *testing.T) {
	f := newGlobalFixture(t)
	if err := InstallGlobal(f.options(GlobalServiceChain), f.host); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(f.host.BinDir, "cosmovisor")
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != "binary cosmovisor" {
		t.Fatalf("installed cosmovisor = %q (%v), want the tarball's cosmovisor file", got, err)
	}
	if !slices.Contains(f.chowns, chownCall{dst, 0, 0}) {
		t.Error("cosmovisor was not made root's")
	}
	if _, err := os.Stat(filepath.Join(f.host.BinDir, "LICENSE")); err == nil {
		t.Error("another file of the tarball was installed")
	}
}

func TestInstallGlobal_aTarballThatIsNotThePinnedReleaseIsRefused(t *testing.T) {
	f := newGlobalFixture(t)
	tampered := cosmovisorTarball(t, "backdoored cosmovisor")
	if err := os.WriteFile(filepath.Join(f.staged, constants.CosmovisorTarball("amd64")), tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	err := InstallGlobal(f.options(GlobalServiceChain), f.host)
	if err == nil || !strings.Contains(err.Error(), "not the pinned") {
		t.Fatalf("err = %v, want a digest refusal", err)
	}
	if _, statErr := os.Stat(filepath.Join(f.host.BinDir, "cosmovisor")); !os.IsNotExist(statErr) {
		t.Error("an unverified cosmovisor was installed")
	}
	if _, statErr := os.Stat(f.host.BinDir); !os.IsNotExist(statErr) || len(f.node.changes()) != 0 {
		t.Errorf("the host changed before the digest refusal: %v", f.node.calls)
	}
	if len(f.stages) != 0 || f.node.named("systemctl") != nil {
		t.Error("the install went on after the digest refusal")
	}
}

func TestInstallGlobal_cosmovisorTarballProblemsNameTheFix(t *testing.T) {
	f := newGlobalFixture(t)
	tarPath := filepath.Join(f.staged, constants.CosmovisorTarball("amd64"))
	if err := os.Remove(tarPath); err != nil {
		t.Fatal(err)
	}
	if err := InstallGlobal(f.options(GlobalServiceChain), f.host); err == nil || !strings.Contains(err.Error(), constants.CosmovisorTarball("amd64")) {
		t.Fatalf("a missing tarball: %v", err)
	}

	f = newGlobalFixture(t)
	f.host.Arch = "riscv64"
	if err := InstallGlobal(f.options(GlobalServiceChain), f.host); err == nil || !strings.Contains(err.Error(), "riscv64") {
		t.Fatalf("an unsupported architecture: %v", err)
	}

	f = newGlobalFixture(t)
	if _, err := extractCosmovisor([]byte("not gzip")); err == nil {
		t.Fatal("garbage was extracted")
	}
	sum := sha256.Sum256(f.tarball)
	if hex.EncodeToString(sum[:]) != f.host.CosmovisorPins["amd64"] {
		t.Fatal("the fixture pin drifted")
	}
}

func TestExtractCosmovisor_needsTheCosmovisorFile(t *testing.T) {
	if got, err := extractCosmovisor(cosmovisorTarball(t, "bin")); err != nil || string(got) != "bin" {
		t.Fatalf("extract = %q, %v", got, err)
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "LICENSE", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(tw.Close(), gz.Close()); err != nil {
		t.Fatal(err)
	}
	if _, err := extractCosmovisor(buf.Bytes()); err == nil || !strings.Contains(err.Error(), "no cosmovisor file") {
		t.Fatalf("a tarball without cosmovisor: %v", err)
	}
}

func TestInstallGlobal_stagesTheStagedOramadAsTheGenesisBinary(t *testing.T) {
	f := newGlobalFixture(t)
	if err := InstallGlobal(f.options(GlobalServiceChain), f.host); err != nil {
		t.Fatal(err)
	}
	oramad, err := os.ReadFile(filepath.Join(f.staged, "oramad"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(oramad)
	want := []stageCall{{
		src: filepath.Join(f.staged, "oramad"), sum: hex.EncodeToString(sum[:]),
		companions: []StagedFile{{Name: "orama-orchard-verifier", Src: filepath.Join(f.staged, "orama-orchard-verifier"), Sum: f.verifierSum}},
	}}
	if !reflect.DeepEqual(f.stages, want) {
		t.Fatalf("stage calls = %v, want %v", f.stages, want)
	}
}

func TestInstallGlobal_noChainHomeMeansNothingToStage(t *testing.T) {
	f := newGlobalFixture(t)
	f.freshHome(t)
	err := InstallGlobal(f.options(GlobalServiceChain), f.host)
	if err == nil || !strings.Contains(err.Error(), "--init-chain") {
		t.Fatalf("err = %v, want a refusal naming --init-chain", err)
	}
	if f.node.named("systemctl") != nil {
		t.Error("units were enabled for a chain that cannot start")
	}
	if _, statErr := os.Stat(f.host.BinDir); !os.IsNotExist(statErr) || len(f.node.changes()) != 0 {
		t.Errorf("the host changed before the refusal: %v", f.node.calls)
	}
}

func TestStagedBinaryHasSum_sameBytesMissingAndDifferent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "oramad")
	sum := sha256.Sum256([]byte("binary"))
	want := hex.EncodeToString(sum[:])
	if same, err := stagedBinaryHasSum(path, want); err != nil || same {
		t.Fatalf("missing: same=%v err=%v", same, err)
	}
	if err := os.WriteFile(path, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if same, err := stagedBinaryHasSum(path, want); err != nil || !same {
		t.Fatalf("same bytes: same=%v err=%v", same, err)
	}
	if err := os.WriteFile(path, []byte("other"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := stagedBinaryHasSum(path, want); err == nil || !strings.Contains(err.Error(), "stage-oramad --upgrade") {
		t.Fatalf("different bytes: %v", err)
	}
}

func TestInstallGlobal_publicKuboIsInitialisedAndConfiguredPublicly(t *testing.T) {
	f := newGlobalFixture(t)
	if err := InstallGlobal(f.options(GlobalServiceChain, GlobalServiceIPFS), f.host); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(f.host.StateDir, "ipfs")
	bin := filepath.Join(f.host.BinDir, "ipfs")
	wantRun := []string{
		"-u orama-ipfs-pub -- " + bin + " --version",
		"-u orama-ipfs-pub -- env HOME=" + repo + " IPFS_PATH=" + repo + " " + bin + " init --profile=server --repo-dir=" + repo,
	}
	if got := f.node.named("runuser"); !slices.Equal(got, wantRun) {
		t.Fatalf("runuser = %v, want %v", got, wantRun)
	}

	raw, err := os.ReadFile(filepath.Join(repo, "config"))
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "swarm.key") {
		t.Error("the public repo names a swarm key")
	}
	if config["Identity"] == nil {
		t.Error("ipfs init's identity was dropped")
	}
	if got := config["Provide"].(map[string]any)["Strategy"]; got != "pinned" {
		t.Errorf("Provide.Strategy = %v", got)
	}
	if got := config["Datastore"].(map[string]any)["StorageMax"]; got != "550GB" {
		t.Errorf("StorageMax = %v, want the 500 GB budget plus 10%%", got)
	}
	if !strings.Contains(string(raw), "/ip4/10.0.0.0/ipcidr/8") || !strings.Contains(string(raw), "127.0.0.1/tcp/31011") {
		t.Error("the config lacks the private-range filters or the loopback RPC")
	}
	if _, err := os.Stat(filepath.Join(repo, "swarm.key")); err == nil {
		t.Error("a swarm.key was written")
	}

	token, err := os.ReadFile(filepath.Join(repo, "api-token"))
	if err != nil || len(strings.TrimSpace(string(token))) < 32 {
		t.Fatalf("token = %q (%v)", token, err)
	}
	if info, _ := os.Stat(filepath.Join(repo, "api-token")); info.Mode().Perm() != 0o640 {
		t.Errorf("token mode %v, want 0640 so only the RPC group reads it", info.Mode().Perm())
	}
	if info, _ := os.Stat(filepath.Join(repo, "config")); info.Mode().Perm() != 0o600 {
		t.Errorf("config mode %v", info.Mode().Perm())
	}
	if info, _ := os.Stat(repo); info.Mode().Perm() != 0o750 {
		t.Errorf("repo dir mode %v, want 0750", info.Mode().Perm())
	}
	env, err := os.ReadFile(filepath.Join(repo, "gc.env"))
	if err != nil || string(env) != "IPFS_API_AUTH=bearer:"+strings.TrimSpace(string(token))+"\n" {
		t.Fatalf("gc.env = %q (%v)", env, err)
	}
	for _, c := range []chownCall{
		{repo, 990, 995},
		{filepath.Join(repo, "api-token"), 990, 995},
		{filepath.Join(repo, "config"), 990, 991},
		{filepath.Join(repo, "gc.env"), 990, 991},
	} {
		if !slices.Contains(f.chowns, c) {
			t.Errorf("missing chown %v", c)
		}
	}
}

func TestInstallGlobal_publicKuboSecondRunKeepsTheTokenAndIdentity(t *testing.T) {
	f := newGlobalFixture(t)
	opts := f.options(GlobalServiceChain, GlobalServiceIPFS)
	if err := InstallGlobal(opts, f.host); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(f.host.StateDir, "ipfs")
	token1, _ := os.ReadFile(filepath.Join(repo, "api-token"))
	f.node.calls = nil
	opts.PublicStorageBytes = 1_000_000_000_000
	if err := InstallGlobal(opts, f.host); err != nil {
		t.Fatal(err)
	}
	token2, _ := os.ReadFile(filepath.Join(repo, "api-token"))
	if string(token1) != string(token2) {
		t.Error("a second install rotated the RPC token")
	}
	for _, call := range f.node.named("runuser") {
		if strings.Contains(call, " init ") {
			t.Errorf("a repo that exists was initialised again: %s", call)
		}
	}
	raw, _ := os.ReadFile(filepath.Join(repo, "config"))
	if !strings.Contains(string(raw), "12D3KooWtest") || !strings.Contains(string(raw), "1100GB") {
		t.Errorf("second run lost the identity or did not apply the new budget:\n%s", raw)
	}
}

func TestInstallGlobal_publicKuboRefusesAWrongKuboVersion(t *testing.T) {
	f := newGlobalFixture(t)
	f.node.kuboVersion = "ipfs version 0.30.0\n"
	err := InstallGlobal(f.options(GlobalServiceChain, GlobalServiceIPFS), f.host)
	if err == nil || !strings.Contains(err.Error(), constants.IPFSKuboVersion) {
		t.Fatalf("err = %v, want the pinned version named", err)
	}
	if _, err := os.Stat(filepath.Join(f.host.StateDir, "ipfs", "config")); err == nil {
		t.Error("a repo was created for the wrong Kubo")
	}
}

func TestInstallGlobal_publicKuboRefusesARepoThatJoinedAPrivateSwarm(t *testing.T) {
	f := newGlobalFixture(t)
	repo := filepath.Join(f.host.StateDir, "ipfs")
	if err := os.MkdirAll(repo, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "config"), []byte(`{"Identity":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "swarm.key"), []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := InstallGlobal(f.options(GlobalServiceChain, GlobalServiceIPFS), f.host)
	if err == nil || !strings.Contains(err.Error(), "swarm.key") {
		t.Fatalf("err = %v, want the swarm key refused", err)
	}
}

func TestInstallGlobal_ipfsWritesTheGCTimerAndEnablesOnlyWhatCanBeEnabled(t *testing.T) {
	f := newGlobalFixture(t)
	if err := InstallGlobal(f.options(GlobalServiceChain, GlobalServiceIPFS), f.host); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		constants.GlobalIPFSUnit: RenderGlobalIPFSUnit(),
		globalIPFSGCUnit:         RenderGlobalIPFSGCUnit("127.0.0.1"),
		globalIPFSGCTimer:        RenderGlobalIPFSGCTimer(),
	} {
		got, err := os.ReadFile(filepath.Join(f.host.UnitDir, name))
		if err != nil || string(got) != want {
			t.Errorf("%s not written as rendered (%v)", name, err)
		}
	}
	enabled := f.node.named("systemctl")
	if !slices.Contains(enabled, "enable "+globalIPFSGCTimer) || slices.Contains(enabled, "enable "+globalIPFSGCUnit) {
		t.Errorf("systemctl = %v: enable the timer, never the oneshot", enabled)
	}
	gc := RenderGlobalIPFSGCUnit("127.0.0.1")
	if !strings.Contains(gc, "EnvironmentFile=/var/lib/orama-global/ipfs/gc.env\n") ||
		!strings.Contains(gc, "Environment=IPFS_API=/ip4/127.0.0.1/tcp/31011\n") ||
		mustDirective(t, gc, "ExecStart") != globalBinDir+"/orama node ipfs-gc" {
		t.Errorf("the GC unit does not authenticate to the daemon's RPC:\n%s", gc)
	}
	if !strings.Contains(mustDirective(t, gc, "After"), constants.GlobalIPFSUnit) {
		t.Error("GC is not ordered after the daemon it collects through")
	}
}

func TestGlobalIPFSUnit_repoIsReadableByTheRPCGroupOnly(t *testing.T) {
	unit := RenderGlobalIPFSUnit()
	if mustDirective(t, unit, "User") != globalIPFSUser || mustDirective(t, unit, "Group") != globalIPFSRPCGroup {
		t.Errorf("ipfs unit account:\n%s", unit)
	}
	if mustDirective(t, unit, "StateDirectoryMode") != "0750" {
		t.Error("the RPC group cannot traverse the repo to reach the token")
	}
	if !strings.Contains(unit, "UMask=0077") {
		t.Error("files the daemon writes are not private")
	}
	provider := RenderGlobalProviderUnit("127.0.0.1")
	exec := mustDirective(t, provider, "ExecStart")
	if !strings.Contains(exec, "--ipfs-api http://127.0.0.1:31011 --ipfs-token-file /var/lib/orama-global/ipfs/api-token") {
		t.Errorf("provider ExecStart = %q", exec)
	}
}

func TestInstallGlobal_indexerIsOptInLoopbackOnly(t *testing.T) {
	f := newGlobalFixture(t)
	if err := InstallGlobal(f.options(GlobalServiceChain, GlobalServiceIndexer), f.host); err != nil {
		t.Fatal(err)
	}
	if !f.node.users["orama-indexer"] {
		t.Error("the indexer account was not created")
	}
	got, err := os.ReadFile(filepath.Join(f.host.UnitDir, constants.GlobalIndexerUnit))
	if err != nil || string(got) != RenderGlobalIndexerUnit() {
		t.Fatalf("indexer unit (%v)", err)
	}
	if !slices.Contains(f.node.named("systemctl"), "enable "+constants.GlobalIndexerUnit) {
		t.Error("the indexer unit was not enabled")
	}
	wantUFW := []string{"status", "allow 31000/tcp comment orama-global", "allow 31000/udp comment orama-global"}
	if ufw := f.node.named("ufw"); !slices.Equal(ufw, wantUFW) {
		t.Errorf("ufw = %v: the indexer listens on loopback and opens nothing", ufw)
	}

	plain := newGlobalFixture(t)
	if err := InstallGlobal(plain.options(GlobalServiceChain), plain.host); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(plain.host.UnitDir, constants.GlobalIndexerUnit)); !os.IsNotExist(err) {
		t.Error("the indexer was installed without being asked for")
	}
}

func TestInstallGlobal_aDifferentGenesisBinaryIsRefusedBeforeAnyBinaryIsReplaced(t *testing.T) {
	f := newGlobalFixture(t)
	bin := filepath.Join(f.host.ChainHome, "cosmovisor", "genesis", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "oramad"), []byte("the running oramad"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := InstallGlobal(f.options(GlobalServiceChain), f.host)
	if err == nil || !strings.Contains(err.Error(), "stage-oramad --upgrade") {
		t.Fatalf("err = %v, want a refusal naming stage-oramad", err)
	}
	if _, statErr := os.Stat(f.host.BinDir); !os.IsNotExist(statErr) {
		t.Error("binaries were installed before the refusal")
	}
	if len(f.node.changes()) != 0 || f.node.named("systemctl") != nil {
		t.Errorf("the host changed before the refusal: %v", f.node.calls)
	}
}

func TestGlobalIPFSUnit_onlyTheDaemonMayUseNetlink(t *testing.T) {
	if !strings.Contains(RenderGlobalIPFSUnit(), "RestrictAddressFamilies=AF_INET AF_UNIX AF_NETLINK\n") {
		t.Error("libp2p reads interfaces over netlink; the daemon cannot start without it")
	}
	for name, unit := range map[string]string{
		"gc": RenderGlobalIPFSGCUnit("127.0.0.1"), "provider": RenderGlobalProviderUnit("127.0.0.1"), "chain": RenderGlobalChainUnit(""),
	} {
		if strings.Contains(unit, "AF_NETLINK") {
			t.Errorf("%s may use netlink", name)
		}
	}
}
