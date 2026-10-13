package auth

import (
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

// A node's key is generated once and kept. Generating a second one would make
// the cluster refuse the node — its recorded key would no longer match — and
// the reason would be invisible.
func TestLoadOrCreateNodeKey_isGeneratedOnceAndKept(t *testing.T) {
	dir := t.TempDir()

	first, err := LoadOrCreateNodeKey(dir)
	if err != nil {
		t.Fatalf("LoadOrCreateNodeKey: %v", err)
	}
	second, err := LoadOrCreateNodeKey(dir)
	if err != nil {
		t.Fatalf("LoadOrCreateNodeKey again: %v", err)
	}
	if first.PublicKey() != second.PublicKey() {
		t.Error("a second start generated a different key, so the cluster would refuse this node")
	}
}

// The file is the node's identity: anything that can read it can speak as this
// node until an operator revokes it.
func TestLoadOrCreateNodeKey_isUnreadableToAnyoneElse(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadOrCreateNodeKey(dir); err != nil {
		t.Fatalf("LoadOrCreateNodeKey: %v", err)
	}

	info, err := os.Stat(NodeKeyPath(dir))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("the node key is mode %o, want 600", mode)
	}
}

// A key that exists and cannot be read is an error, never a reason to generate
// a second one: a node that quietly re-keyed would be refused by the cluster
// and nothing would say why.
func TestLoadOrCreateNodeKey_aDamagedKeyIsNotReplaced(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "secrets"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := NodeKeyPath(dir)
	if err := os.WriteFile(path, []byte("this is not a key"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := LoadOrCreateNodeKey(dir)
	if err == nil {
		t.Fatal("a damaged key was silently replaced with a new one")
	}
	// The error has to say the key could not be *read*. A node that fell
	// through to generating one would fail on the O_EXCL write instead, which
	// looks like a permissions problem and sends whoever reads it to the wrong
	// place — the fix is to repair the key or re-admit the node.
	if !strings.Contains(err.Error(), "could not be read") {
		t.Errorf("the error does not say the key is unreadable, so it reads as a write failure: %v", err)
	}

	kept, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(kept) != "this is not a key" {
		t.Error("the damaged key was overwritten; there is now nothing to repair")
	}
}

// A key on disk round-trips, so a restart carries the same identity.
func TestLoadOrCreateNodeKey_survivesARestart(t *testing.T) {
	dir := t.TempDir()
	first, err := LoadOrCreateNodeKey(dir)
	if err != nil {
		t.Fatalf("LoadOrCreateNodeKey: %v", err)
	}

	// Sign before, verify after — the identity, not just the bytes.
	body := []byte(`{}`)
	r := httptest.NewRequest(http.MethodPost, "/v1/internal/node/register", nil)
	now := time.Now()
	if err := SignNodeAPI(first, r, "node-a", body, now); err != nil {
		t.Fatalf("sign: %v", err)
	}

	reloaded, err := LoadOrCreateNodeKey(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	pub, err := ParseNodePublicKey(reloaded.PublicKey())
	if err != nil {
		t.Fatalf("ParseNodePublicKey: %v", err)
	}
	if _, _, ok := VerifyNodeAPI(func(string) (NodeStampVerifier, error) { return pub, nil }, r, body, now); !ok {
		t.Error("a stamp made before a restart was not verified by the key found after it")
	}
}

// A node signs with a key the cluster never holds, so the cluster cannot
// impersonate it and neither can another node.
func TestNodeKeyPair_signsSomethingOnlyItsOwnPublicKeyVerifies(t *testing.T) {
	mine, err := NewNodeKeyPair()
	if err != nil {
		t.Fatalf("NewNodeKeyPair: %v", err)
	}
	theirs, err := NewNodeKeyPair()
	if err != nil {
		t.Fatalf("NewNodeKeyPair: %v", err)
	}
	if mine.PublicKey() == theirs.PublicKey() {
		t.Fatal("two nodes generated the same key")
	}

	body := []byte(`{"ip_address":"203.0.113.7"}`)
	now := time.Now()
	r := httptest.NewRequest(http.MethodPost, "/v1/internal/node/register", nil)
	if err := SignNodeAPI(mine, r, "node-a", body, now); err != nil {
		t.Fatalf("sign: %v", err)
	}

	minePub, _ := ParseNodePublicKey(mine.PublicKey())
	theirsPub, _ := ParseNodePublicKey(theirs.PublicKey())

	if _, _, ok := VerifyNodeAPI(func(string) (NodeStampVerifier, error) { return minePub, nil }, r, body, now); !ok {
		t.Error("a node's own key did not verify its own stamp")
	}
	if _, _, ok := VerifyNodeAPI(func(string) (NodeStampVerifier, error) { return theirsPub, nil }, r, body, now); ok {
		t.Error("another node's key verified this node's stamp")
	}

	// Nothing derived from the cluster secret is in this path at all: the only
	// thing that verifies a node's stamp is the key that node enrolled.
}

// The stamp binds the same things whichever credential made it: a signature
// lifted onto a different body, node or path is refused exactly as a MAC is.
func TestNodeKeyPair_theSignatureIsBoundToTheRequest(t *testing.T) {
	own, err := NewNodeKeyPair()
	if err != nil {
		t.Fatalf("NewNodeKeyPair: %v", err)
	}
	pub, err := ParseNodePublicKey(own.PublicKey())
	if err != nil {
		t.Fatalf("ParseNodePublicKey: %v", err)
	}
	verifier := func(string) (NodeStampVerifier, error) { return pub, nil }

	now := time.Now()
	signed := []byte(`{"ip_address":"203.0.113.7"}`)
	r := httptest.NewRequest(http.MethodPost, "/v1/internal/node/register", nil)
	if err := SignNodeAPI(own, r, "node-a", signed, now); err != nil {
		t.Fatalf("sign: %v", err)
	}

	if _, _, ok := VerifyNodeAPI(verifier, r, []byte(`{"ip_address":"9.9.9.9"}`), now); ok {
		t.Error("a signature over one body was accepted for another")
	}
	r.Header.Set(NodeIDHeader, "node-b")
	if _, _, ok := VerifyNodeAPI(verifier, r, signed, now); ok {
		t.Error("a signature naming one node was accepted as another")
	}
}

// A public key is read as it is stored, and anything else is refused rather
// than producing a verifier that accepts nothing and says nothing.
func TestParseNodePublicKey_refusesWhatIsNotOne(t *testing.T) {
	for name, encoded := range map[string]string{
		"empty":          "",
		"not base64":     "!!!!",
		"too short":      "aGVsbG8=",
		"too long":       strings.Repeat("QQ==", 40),
		"an rsa modulus": "MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8A",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseNodePublicKey(encoded); err == nil {
				t.Errorf("%q was read as a node public key", encoded)
			}
		})
	}

	// A real one, with the whitespace a file or a JSON field might carry.
	own, err := NewNodeKeyPair()
	if err != nil {
		t.Fatalf("NewNodeKeyPair: %v", err)
	}
	if _, err := ParseNodePublicKey("  " + own.PublicKey() + "\n"); err != nil {
		t.Errorf("a real public key with whitespace was refused: %v", err)
	}
}

// A private key of the wrong size is not a node key. Adopting one would produce
// a signer whose stamps nothing verifies.
func TestNodeKeyPairFrom_refusesAKeyOfTheWrongSize(t *testing.T) {
	if _, err := NodeKeyPairFrom(make([]byte, 32)); err == nil {
		t.Error("a 32-byte private key was adopted as an Ed25519 one")
	}
	if _, err := NodeKeyPairFrom(nil); err == nil {
		t.Error("a nil private key was adopted")
	}
}

// A peer id that does not carry its public key — an older, hashed identity —
// has nothing to check an enrolment against. Resolving it to something that
// verifies would make the check optional for anyone who could produce one.
func TestNodeIdentityVerifier_anIdThatCarriesNoKeyResolvesToNothing(t *testing.T) {
	// A sha256-multihash peer id: the key is hashed into it, not carried.
	if _, err := NodeIdentityVerifier("QmYyQSo1c1Ym7orWxLYvCrM2EmxFTANf8wXmmE7DWjhx5N"); err == nil {
		t.Error("a peer id that carries no public key resolved to a verifier")
	}
	if _, err := NodeIdentityVerifier("not-a-peer-id"); err == nil {
		t.Error("something that is not a peer id resolved to a verifier")
	}
	if _, err := NodeIdentityVerifier(""); err == nil {
		t.Error("an empty node id resolved to a verifier")
	}
}

// The key inside a peer id verifies that node's stamps and no other's. This is
// what authenticates an enrolment with nothing recorded, and it is why there is
// no trust-on-first-use window at all.
func TestNodeIdentityVerifier_verifiesOnlyTheNodeTheIdNames(t *testing.T) {
	minePriv, mineID := libp2pIdentity(t)
	theirsPriv, theirsID := libp2pIdentity(t)

	now := time.Now()
	body := []byte(`{"public_key":"..."}`)
	r := httptest.NewRequest(http.MethodPost, "/v1/internal/node/enrol-key", nil)
	if err := SignNodeAPI(NodeIdentitySigner(minePriv), r, mineID, body, now); err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, _, ok := VerifyNodeAPI(NodeIdentityVerifier, r, body, now); !ok {
		t.Error("a node's own libp2p identity did not verify its own enrolment")
	}

	// The attack: another machine, holding its own identity — and, in the real
	// world, the cluster secret too — stamping for a node id it does not own.
	forged := httptest.NewRequest(http.MethodPost, "/v1/internal/node/enrol-key", nil)
	if err := SignNodeAPI(NodeIdentitySigner(theirsPriv), forged, mineID, body, now); err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, _, ok := VerifyNodeAPI(NodeIdentityVerifier, forged, body, now); ok {
		t.Error("one node signed an enrolment for another node's id")
	}

	// And the honest case for that other node still works, so the refusal above
	// is the identity check and not something broken.
	theirs := httptest.NewRequest(http.MethodPost, "/v1/internal/node/enrol-key", nil)
	if err := SignNodeAPI(NodeIdentitySigner(theirsPriv), theirs, theirsID, body, now); err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, _, ok := VerifyNodeAPI(NodeIdentityVerifier, theirs, body, now); !ok {
		t.Error("a second node could not enrol with its own identity")
	}
}

// Signing needs a key. A nil identity produces no signer rather than one that
// panics on first use.
func TestNodeIdentitySigner_withoutAKeyIsNothing(t *testing.T) {
	if NodeIdentitySigner(nil) != nil {
		t.Error("a nil libp2p identity produced a signer")
	}
}

// A half-written key file would be read on the next start, fail to parse, and
// be refused rather than replaced — so the node could never register again
// until a human deleted it. The key is renamed into place, so what is at the
// path is either absent or complete.
func TestWriteNodeKey_leavesNothingHalfWritten(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadOrCreateNodeKey(dir); err != nil {
		t.Fatalf("LoadOrCreateNodeKey: %v", err)
	}

	entries, err := os.ReadDir(filepath.Join(dir, "secrets"))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("a temporary key file was left behind: %s", e.Name())
		}
	}

	// And what landed is a complete, loadable key.
	if _, err := LoadOrCreateNodeKey(dir); err != nil {
		t.Errorf("the key that was written could not be read back: %v", err)
	}
}

// A key file anyone else can read is a machine that can be impersonated. The
// mode this process writes is not the question — a file restored from a backup
// or unpacked by a deploy is.
func TestLoadOrCreateNodeKey_refusesAKeyOthersCanRead(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadOrCreateNodeKey(dir); err != nil {
		t.Fatalf("LoadOrCreateNodeKey: %v", err)
	}
	if err := os.Chmod(NodeKeyPath(dir), 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	_, err := LoadOrCreateNodeKey(dir)
	if err == nil {
		t.Fatal("a world-readable node key was used")
	}
	if !strings.Contains(err.Error(), "must be") {
		t.Errorf("the error does not say what the mode should be: %v", err)
	}
}

// libp2pIdentity generates an identity and the peer id that carries its public
// key.
func libp2pIdentity(t *testing.T) (crypto.PrivKey, string) {
	t.Helper()
	priv, pub, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateEd25519Key: %v", err)
	}
	id, err := peer.IDFromPublicKey(pub)
	if err != nil {
		t.Fatalf("IDFromPublicKey: %v", err)
	}
	return priv, id.String()
}
