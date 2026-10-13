package installers

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/gateway/clientkey"
)

// The gateway believes the X-Forwarded-For Caddy appends only on a request that carries the node's
// proof. Caddy must therefore set the header on everything it forwards to the gateway, which
// replaces whatever the client sent, and must never hand the secret to another service.
func TestGenerateCaddyfile_setsTheProofOnEveryGatewayBlock(t *testing.T) {
	ci := newTestCaddyInstaller()
	ci.EnableNtfyProxy("push.dbrs.space")
	cf := ci.generateCaddyfile("node1.dbrs.space", "admin@dbrs.space",
		"http://localhost:10104/v1/internal/acme", "dbrs.space", "")

	set := `        header_up ` + clientkey.ProofHeader + ` "{file.` + CaddyProofPath + `}"` + "\n"
	strip := "        header_up -" + clientkey.ProofHeader + "\n"
	gatewayBlocks := strings.Count(cf, "reverse_proxy localhost:10104 {")
	if gatewayBlocks == 0 {
		t.Fatal("no reverse_proxy to the gateway at all: this test proves nothing")
	}
	if got := strings.Count(cf, set); got != gatewayBlocks {
		t.Errorf("the proof is set in %d of %d gateway blocks", got, gatewayBlocks)
	}

	// The block that fronts ntfy strips a copy a client sent and never carries the secret.
	ntfy := cf[strings.Index(cf, "push.dbrs.space {"):]
	ntfy = ntfy[:strings.Index(ntfy, "\n}\n")]
	if !strings.Contains(ntfy, strip) {
		t.Errorf("the ntfy block does not strip the proof header:\n%s", ntfy)
	}
	if strings.Contains(ntfy, CaddyProofPath) {
		t.Errorf("the ntfy block carries the proof:\n%s", ntfy)
	}
	if got := strings.Count(cf, strip); got != 1 {
		t.Errorf("the proof header is stripped in %d blocks, want only ntfy's", got)
	}
}

// A client that sends the header itself is overwritten, not appended to: the directive sets the
// field (no `+`/`?` prefix, which would add to it or keep the client's), and no block both sets
// and deletes it (a delete is applied after a set, so the proof would be lost).
func TestGenerateCaddyfile_proofDirectiveReplacesTheClientsCopy(t *testing.T) {
	cf := newTestCaddyInstaller().generateCaddyfile("node1.dbrs.space", "admin@dbrs.space",
		"http://localhost:10104/v1/internal/acme", "dbrs.space", "")
	for _, line := range strings.Split(cf, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "header_up" {
			continue
		}
		name := strings.TrimLeft(fields[1], "+-?>")
		if name != clientkey.ProofHeader {
			continue
		}
		if fields[1] != clientkey.ProofHeader && fields[1] != "-"+clientkey.ProofHeader {
			t.Errorf("the proof header is handled with a modifier that adds to or keeps a client's copy: %q", line)
		}
	}
	if strings.Contains(cf, "header_up +"+clientkey.ProofHeader) || strings.Contains(cf, "header_up ?"+clientkey.ProofHeader) {
		t.Error("a modifier on the proof header lets a client's copy through")
	}
}

func TestCaddyProofPath_isInRootsEtcCaddy(t *testing.T) {
	if CaddyProofPath != constants.CaddyProofFile {
		t.Errorf("the installer writes %s and the gateway reads %s", CaddyProofPath, constants.CaddyProofFile)
	}
	if filepath.Dir(CaddyProofPath) != "/etc/caddy" {
		t.Errorf("the proof is in %s, not Caddy's root-owned directory", filepath.Dir(CaddyProofPath))
	}
}

var proofContent = regexp.MustCompile(`^[0-9a-f]{64}\n$`)

func TestWriteCaddyProof_randomPerWriteAndHeldBack0600UntilRestricted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "proof")
	var modeAtRestrict os.FileMode
	restrict := func(p string) error {
		info, err := os.Stat(p)
		if err != nil {
			return err
		}
		modeAtRestrict = info.Mode().Perm()
		return os.Chmod(p, 0o640)
	}

	if err := writeCaddyProof(path, restrict); err != nil {
		t.Fatalf("writeCaddyProof: %v", err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !proofContent.Match(first) {
		t.Errorf("the proof is not 64 hex characters and a newline: %d bytes", len(first))
	}
	if modeAtRestrict != 0o600 {
		t.Errorf("the file was %v when it was handed to the group, want 0600", modeAtRestrict)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o640 {
		t.Errorf("final mode = %v, want 0640", info.Mode().Perm())
	}

	// Every install and upgrade makes a new one.
	if err := writeCaddyProof(path, restrict); err != nil {
		t.Fatalf("writeCaddyProof again: %v", err)
	}
	second, _ := os.ReadFile(path)
	if string(first) == string(second) {
		t.Error("a second write left the same secret")
	}
}

func TestWriteCaddyProof_errorsNameThePathNotTheSecret(t *testing.T) {
	dir := t.TempDir()
	if err := writeCaddyProof(filepath.Join(dir, "absent-dir", "proof"), func(string) error { return nil }); err == nil ||
		!strings.Contains(err.Error(), "absent-dir") {
		t.Errorf("a write into a missing directory: %v", err)
	}

	path := filepath.Join(dir, "proof")
	if err := writeCaddyProof(path, func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	restrictErr := errors.New("no such group")
	err := writeCaddyProof(path, func(string) error { return restrictErr })
	if !errors.Is(err, restrictErr) {
		t.Fatalf("the restriction's failure is lost: %v", err)
	}
	if hexSecret.MatchString(err.Error()) {
		t.Errorf("the error carries a secret: %v", err)
	}
	// The new secret never replaced the old one: Caddy and the gateway keep proving with it, and
	// nothing of the failed write is left beside it.
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Error("a write that failed replaced the proof")
	}
	if _, statErr := os.Stat(path + ".new"); !os.IsNotExist(statErr) {
		t.Errorf("the failed write left %s.new behind", path)
	}
}

// hexSecret is the form of a proof: 64 hex characters.
var hexSecret = regexp.MustCompile(`[0-9a-f]{64}`)
