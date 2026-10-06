package build

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/archivetrust"
	"github.com/DeBrosOfficial/network/pkg/rwagent"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

// fakeAgent serves the RootWallet agent's wallet routes on a Unix socket, the
// way the desktop app does: /v1/wallet/sign is EIP-191 personal_sign with v in
// 27/28, written here from the EIP rather than from the verifier under test.
type fakeAgent struct {
	key      *ecdsa.PrivateKey
	reported string // the address /v1/wallet/address answers with
	signed   []string
}

func startFakeAgent(t *testing.T, a *fakeAgent) *rwagent.Client {
	t.Helper()
	// A short directory: a Unix socket path is limited to about 100 bytes.
	dir, err := os.MkdirTemp("", "rw")
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "agent.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/wallet/address", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"ok":true,"data":{"address":%q,"chain":"evm"}}`, a.reported)
	})
	mux.HandleFunc("POST /v1/wallet/sign", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Message, Chain, Purpose string }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Chain != "evm" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		// The agent's rule: a message in the archive format is signed only
		// under the orama-archive purpose, never as a plain wallet:sign.
		if strings.HasPrefix(req.Message, "Orama build archive v1\n") && req.Purpose != rwagent.PurposeOramaArchive {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"ok":false,"code":"SIGN_PURPOSE_REQUIRED","error":"archive messages need purpose orama-archive"}`)
			return
		}
		a.signed = append(a.signed, req.Message)
		digest := ethcrypto.Keccak256([]byte(fmt.Sprintf("\x19Ethereum Signed Message:\n%d%s", len(req.Message), req.Message)))
		sig, err := ethcrypto.Sign(digest, a.key)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		sig[64] += 27
		fmt.Fprintf(w, `{"ok":true,"data":{"signature":"0x%s"}}`, hex.EncodeToString(sig))
	})
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	t.Cleanup(func() {
		srv.Close()
		os.RemoveAll(dir)
	})
	return rwagent.New(sock)
}

func newKey(t *testing.T) (*ecdsa.PrivateKey, string) {
	t.Helper()
	key, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	return key, ethcrypto.PubkeyToAddress(key.PublicKey).Hex()
}

// stagedBuilder is a Builder whose build directory already holds what the
// compile steps produce.
func stagedBuilder(t *testing.T, flags *Flags, agent archiveSigner) *Builder {
	t.Helper()
	tmp := t.TempDir()
	b := &Builder{flags: flags, agent: agent, tmpDir: tmp, binDir: filepath.Join(tmp, "bin"),
		version: "9.9.9", commit: "abc", date: "2026-09-26T00:00:00Z"}
	for rel, body := range map[string]string{
		"bin/orama":                         "cli",
		"bin/orama-node":                    "node",
		"systemd/orama-namespace-x.service": "[Unit]\n",
	} {
		p := filepath.Join(tmp, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return b
}

// buildSealed runs the manifest, signing and archive steps of Build.
func buildSealed(t *testing.T, b *Builder) (string, error) {
	t.Helper()
	signer, err := b.signingPlan()
	if err != nil {
		return "", err
	}
	m, err := b.generateManifest()
	if err != nil {
		t.Fatal(err)
	}
	manifestJSON, sig, err := sealManifest(m, b.agent, signer)
	if err != nil {
		return "", err
	}
	out := filepath.Join(t.TempDir(), "orama.tar.gz")
	if err := b.createArchive(out, m, manifestJSON, sig); err != nil {
		t.Fatal(err)
	}
	return out, nil
}

// The signer (build, through the agent) and the verifier (every node) must
// agree byte for byte. This goes through the real agent client and socket
// protocol and ends in the verifier a node runs.
func TestBuild_signedArchiveVerifiesOnANode(t *testing.T) {
	key, addr := newKey(t)
	agent := &fakeAgent{key: key, reported: addr}
	b := stagedBuilder(t, &Flags{Arch: "amd64"}, startFakeAgent(t, agent))

	archive, err := buildSealed(t, b)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	// createArchive leaves the build directory laid out as the archive
	// extracts: bin/, systemd/, manifest.json, manifest.sig.
	v, err := archivetrust.VerifyTree(b.tmpDir, []string{strings.ToLower(addr)})
	if err != nil {
		t.Fatalf("a node refused the build: %v", err)
	}
	if v.Signer != strings.ToLower(addr) || v.Manifest.Checksums["systemd/orama-namespace-x.service"] == "" {
		t.Fatalf("verified %+v", v)
	}
	if len(agent.signed) != 1 || !strings.HasPrefix(agent.signed[0], "Orama build archive v1\nversion: 9.9.9\n") {
		t.Fatalf("the agent was asked to sign %q, want one build message", agent.signed)
	}
	names := tarNames(t, archive)
	for _, want := range []string{archivetrust.ManifestName, archivetrust.SignatureName, "bin/orama", "systemd/orama-namespace-x.service"} {
		if !slices.Contains(names, want) {
			t.Errorf("archive lacks %s: %v", want, names)
		}
	}
}

func TestBuild_signersRotationIsInTheSignedManifest(t *testing.T) {
	key, addr := newKey(t)
	const next = "0x3333333333333333333333333333333333333333"
	flags := &Flags{Arch: "amd64", Signers: []string{addr, "0x" + strings.ToUpper(next[2:])}}
	b := stagedBuilder(t, flags, startFakeAgent(t, &fakeAgent{key: key, reported: addr}))

	if _, err := buildSealed(t, b); err != nil {
		t.Fatalf("build: %v", err)
	}
	v, err := archivetrust.VerifyTree(b.tmpDir, []string{strings.ToLower(addr)})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !slices.Equal(v.Signers, []string{strings.ToLower(addr), next}) {
		t.Fatalf("the signed manifest rotates to %v", v.Signers)
	}
}

func TestBuild_rotationWithoutTheSigningAccountIsRefusedBeforeBuilding(t *testing.T) {
	key, addr := newKey(t)
	flags := &Flags{Arch: "amd64", Signers: []string{"0x3333333333333333333333333333333333333333"}}
	b := &Builder{flags: flags, agent: startFakeAgent(t, &fakeAgent{key: key, reported: addr})}
	if _, err := b.signingPlan(); err == nil || !strings.Contains(err.Error(), "must include") {
		t.Fatalf("a rotation nodes would refuse was accepted: %v", err)
	}
}

func TestGenerateManifest_listsPackages(t *testing.T) {
	b := stagedBuilder(t, &Flags{Arch: "amd64", Unsigned: true}, unreachableAgent{})
	if err := os.MkdirAll(filepath.Join(b.tmpDir, "packages"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b.tmpDir, "packages", "tool.deb"), []byte("deb"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := b.generateManifest()
	if err != nil {
		t.Fatal(err)
	}
	if m.Checksums["packages/tool.deb"] == "" {
		t.Fatalf("packages/ is archived but not in the manifest: %v", m.Checksums)
	}
}

func TestBuild_agentSigningAsAnotherAccountFailsTheBuild(t *testing.T) {
	key, _ := newKey(t)
	_, reported := newKey(t)
	b := stagedBuilder(t, &Flags{Arch: "amd64"}, startFakeAgent(t, &fakeAgent{key: key, reported: reported}))

	if _, err := buildSealed(t, b); err == nil || !strings.Contains(err.Error(), "signed as") {
		t.Fatalf("a signature by a different account than the agent reports must fail the build: %v", err)
	}
}

func TestBuild_unsignedArchiveHasNoSignatureAndNodesRefuseIt(t *testing.T) {
	b := stagedBuilder(t, &Flags{Arch: "amd64", Unsigned: true}, unreachableAgent{})
	archive, err := buildSealed(t, b)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if slices.Contains(tarNames(t, archive), archivetrust.SignatureName) {
		t.Fatal("an --unsigned archive carries a signature")
	}
	if _, err := archivetrust.VerifyTree(b.tmpDir, []string{"0x1111111111111111111111111111111111111111"}); err == nil {
		t.Fatal("a node accepted an unsigned archive")
	}
}

func TestSigningPlan_refusesBadFlagsBeforeBuilding(t *testing.T) {
	for name, flags := range map[string]*Flags{
		"unsigned rotation": {Unsigned: true, Signers: []string{"0x1111111111111111111111111111111111111111"}},
		"invalid signer":    {Signers: []string{"0x1234"}},
	} {
		t.Run(name, func(t *testing.T) {
			b := &Builder{flags: flags, agent: unreachableAgent{}}
			if _, err := b.signingPlan(); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestSigningPlan_unreachableAgentSaysHowToProceed(t *testing.T) {
	b := &Builder{flags: &Flags{}, agent: unreachableAgent{}}
	_, err := b.signingPlan()
	if !errors.Is(err, rwagent.ErrAgentNotRunning) || !strings.Contains(err.Error(), "--unsigned") {
		t.Fatalf("got %v", err)
	}
}

// unreachableAgent is a RootWallet that is not running.
type unreachableAgent struct{}

func (unreachableAgent) GetAddress(context.Context, string) (*rwagent.WalletAddressData, error) {
	return nil, rwagent.ErrAgentNotRunning
}

func (unreachableAgent) SignForPurpose(context.Context, string, string, string) (*rwagent.WalletSignData, error) {
	return nil, rwagent.ErrAgentNotRunning
}

func tarNames(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return names
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, hdr.Name)
	}
}

// A newline in the version would let the manifest draw its own lines in the
// RootWallet approval dialog; the build refuses to sign it.
func TestSealManifest_refusesAFieldWithAControlCharacter(t *testing.T) {
	key, addr := newKey(t)
	agent := &fakeAgent{key: key, reported: addr}
	m := &Manifest{Version: "1.0\nsigners: none", Arch: "amd64", Checksums: map[string]string{"orama": "ab"}}
	if _, _, err := sealManifest(m, startFakeAgent(t, agent), strings.ToLower(addr)); err == nil {
		t.Fatal("signed a manifest whose version carries a newline")
	}
	if len(agent.signed) != 0 {
		t.Fatal("the agent was asked to sign it")
	}
}

// The version and commit are signed; a build that could not read them used to
// sign "dev" and "unknown" as if they named a real build.
func TestReadVersionAndCommit_failClosed(t *testing.T) {
	dir := t.TempDir()
	b := &Builder{projectDir: filepath.Join(dir, "core")}
	if err := os.MkdirAll(b.projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := b.readVersion(); err == nil {
		t.Error("no VERSION file produced a version")
	}
	if err := os.WriteFile(filepath.Join(dir, "VERSION"), []byte("  \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := b.readVersion(); err == nil {
		t.Error("an empty VERSION file produced a version")
	}
	if err := os.WriteFile(filepath.Join(dir, "VERSION"), []byte("0.200.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if v, err := b.readVersion(); err != nil || v != "0.200.1" {
		t.Errorf("readVersion = %q, %v", v, err)
	}
	if _, err := b.readCommit(); err == nil {
		t.Error("a directory outside any git checkout produced a commit")
	}
}

// A push hook sets GIT_DIR. readCommit must still mean the project directory,
// not that other repository.
func TestReadCommit_ignoresAnInheritedGitDir(t *testing.T) {
	out, err := exec.Command("git", "rev-parse", "--absolute-git-dir").Output()
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	t.Setenv("GIT_DIR", strings.TrimSpace(string(out)))
	b := &Builder{projectDir: t.TempDir()}
	if commit, err := b.readCommit(); err == nil {
		t.Fatalf("readCommit used GIT_DIR and returned %q", commit)
	}
}
