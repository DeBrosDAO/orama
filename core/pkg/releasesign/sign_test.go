package releasesign_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/theupdateframework/go-tuf/v2/metadata"

	"github.com/DeBrosOfficial/network/pkg/releasesign"
	"github.com/DeBrosOfficial/network/pkg/rwagent"
)

// localAgent stands in for the RootWallet agent with a local TEST key. Like
// the real agent it signs only a release payload, only under the release
// purpose.
type localAgent struct {
	key   ed25519.PrivateKey
	calls int
	// answer, when set, replaces the signature the agent returns.
	answer string
}

func (a *localAgent) SignForPurpose(_ context.Context, message, chain, purpose string) (*rwagent.WalletSignData, error) {
	a.calls++
	if purpose != rwagent.PurposeOramaRelease {
		return nil, errors.New("agent: wrong purpose " + purpose)
	}
	if chain != releasesign.SigningChain {
		return nil, errors.New("agent: wrong chain " + chain)
	}
	if !strings.HasPrefix(message, rwagent.ReleasePayloadPrefix) {
		return nil, errors.New("agent: not a TUF payload")
	}
	if a.answer != "" {
		return &rwagent.WalletSignData{Signature: a.answer}, nil
	}
	return &rwagent.WalletSignData{Signature: hex.EncodeToString(ed25519.Sign(a.key, []byte(message)))}, nil
}

func newKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func targetsMeta(t *testing.T) *metadata.Metadata[metadata.TargetsType] {
	t.Helper()
	meta := metadata.Targets(time.Now().Add(24 * time.Hour))
	info, err := metadata.TargetFile().FromBytes("orama-linux-amd64.tar.gz", []byte("archive"), "sha256")
	if err != nil {
		t.Fatal(err)
	}
	meta.Signed.Targets["orama-linux-amd64.tar.gz"] = info
	return meta
}

// rootFor lists pub as the targets key, the way a TUF root ceremony would.
func rootFor(t *testing.T, pub ed25519.PublicKey) *metadata.Metadata[metadata.RootType] {
	t.Helper()
	root := metadata.Root(time.Now().Add(24 * time.Hour))
	key, err := metadata.KeyFromPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	if err := root.Signed.AddKey(key, metadata.TARGETS); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestSign_targetsSignatureVerifiesWithGoTUF(t *testing.T) {
	pub, priv := newKey(t)
	meta := targetsMeta(t)

	if err := releasesign.Sign(context.Background(), &localAgent{key: priv}, meta, pub); err != nil {
		t.Fatal(err)
	}
	if len(meta.Signatures) != 1 {
		t.Fatalf("signatures = %d, want 1", len(meta.Signatures))
	}
	if err := rootFor(t, pub).VerifyDelegate(metadata.TARGETS, meta); err != nil {
		t.Fatalf("go-tuf refused the agent's signature: %v", err)
	}
}

func TestSign_signatureIsOverTheCanonicalPayload(t *testing.T) {
	pub, priv := newKey(t)
	meta := targetsMeta(t)
	if err := releasesign.Sign(context.Background(), &localAgent{key: priv}, meta, pub); err != nil {
		t.Fatal(err)
	}
	payload, err := releasesign.Payload(meta)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(payload), `{"_type":"targets"`) {
		t.Fatalf("payload does not open with the reserved prefix: %.40s", payload)
	}
	if !ed25519.Verify(pub, payload, meta.Signatures[0].Signature) {
		t.Fatal("signature does not verify over the canonical payload")
	}
}

func TestSign_wrongKeyAnswerIsRefused(t *testing.T) {
	pub, _ := newKey(t)
	_, otherPriv := newKey(t)
	meta := targetsMeta(t)

	err := releasesign.Sign(context.Background(), &localAgent{key: otherPriv}, meta, pub)
	if !errors.Is(err, releasesign.ErrBadSignature) {
		t.Fatalf("err = %v, want ErrBadSignature", err)
	}
	if len(meta.Signatures) != 0 {
		t.Fatal("an unverified signature was attached")
	}
}

func TestSign_malformedAnswersAreRefused(t *testing.T) {
	pub, priv := newKey(t)
	for name, answer := range map[string]string{
		"not hex":   "zz",
		"too short": hex.EncodeToString(make([]byte, 63)),
		"too long":  hex.EncodeToString(make([]byte, 65)),
	} {
		t.Run(name, func(t *testing.T) {
			err := releasesign.Sign(context.Background(), &localAgent{key: priv, answer: answer}, targetsMeta(t), pub)
			if !errors.Is(err, releasesign.ErrBadSignature) {
				t.Fatalf("err = %v, want ErrBadSignature", err)
			}
		})
	}
}

func TestSign_badPublicKeyNeverCallsTheAgent(t *testing.T) {
	_, priv := newKey(t)
	agent := &localAgent{key: priv}
	if err := releasesign.Sign(context.Background(), agent, targetsMeta(t), ed25519.PublicKey{1, 2, 3}); err == nil {
		t.Fatal("a short public key was accepted")
	}
	if agent.calls != 0 {
		t.Fatalf("agent called %d times", agent.calls)
	}
}

// The real client has no socket to reach here: the refusal has to come before
// any dial.
func TestSign_realClientRefusesTheArchivePurposeForReleases(t *testing.T) {
	client := rwagent.New(t.TempDir() + "/none.sock")
	payload, err := releasesign.Payload(targetsMeta(t))
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.SignForPurpose(context.Background(), string(payload), releasesign.SigningChain, rwagent.PurposeOramaArchive)
	if !errors.Is(err, rwagent.ErrPurposeMismatch) {
		t.Fatalf("err = %v, want ErrPurposeMismatch", err)
	}
}
