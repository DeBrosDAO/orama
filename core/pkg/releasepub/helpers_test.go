package releasepub

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/secure-systems-lab/go-securesystemslib/cjson"

	"github.com/DeBrosOfficial/network/pkg/releasepub/pubtest"
	"github.com/DeBrosOfficial/network/pkg/releaseverify"
	"github.com/DeBrosOfficial/network/pkg/rwagent"
)

var testNow = time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

// fakeAgent stands in for the RootWallet agent with a TEST key. Like the real
// one it signs only a payload CheckSignable accepts, and it counts approvals.
type fakeAgent struct {
	priv      ed25519.PrivateKey
	payloads  [][]byte
	refuseAt  int
	refuseErr error
}

func newFakeAgent(t *testing.T) *fakeAgent {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &fakeAgent{priv: priv}
}

func (a *fakeAgent) ReleaseKey(context.Context) (ed25519.PublicKey, error) {
	return a.priv.Public().(ed25519.PublicKey), nil
}

func (a *fakeAgent) SignForPurpose(_ context.Context, message, chain, purpose string) (*rwagent.WalletSignData, error) {
	if purpose != rwagent.PurposeOramaRelease || chain != "ed25519" {
		return nil, context.DeadlineExceeded
	}
	if err := CheckSignable([]byte(message)); err != nil {
		return nil, err
	}
	a.payloads = append(a.payloads, []byte(message))
	if a.refuseErr != nil && len(a.payloads) == a.refuseAt {
		return nil, a.refuseErr
	}
	return &rwagent.WalletSignData{Signature: hex.EncodeToString(ed25519.Sign(a.priv, []byte(message)))}, nil
}

// approvals is how many signatures the agent has made.
func (a *fakeAgent) approvals() int { return len(a.payloads) }

// newRepo is a repository directory with a root made by agent.
func newRepo(t *testing.T, agent *fakeAgent) Repo {
	t.Helper()
	repo := Repo{Dir: filepath.Join(t.TempDir(), "repo")}
	if _, err := InitRoot(t.Context(), agent, repo, testNow, nil); err != nil {
		t.Fatal(err)
	}
	return repo
}

// archive writes a release archive for version and arch whose bytes depend on content.
func archive(t *testing.T, version, arch, content string) string {
	t.Helper()
	return pubtest.Archive(t, version, arch, content)
}

func mustChannel(t *testing.T, name string) Channel {
	t.Helper()
	c, err := ParseChannel(name)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func cutParams(repo Repo, agent Agent, channel Channel, archives ...string) CutParams {
	return CutParams{Repo: repo, Channel: channel, Archives: archives, Retention: DefaultRetention, Now: testNow, Agent: agent}
}

// clientView verifies the directory the way a client does.
func clientView(t *testing.T, repo Repo, now time.Time) (*releaseverify.Verified, error) {
	t.Helper()
	meta := releaseverify.Metadata{}
	for name, dst := range map[string]*[]byte{RootFile: &meta.Root, TimestampFile: &meta.Timestamp, SnapshotFile: &meta.Snapshot, TargetsFile: &meta.Targets} {
		data, err := os.ReadFile(filepath.Join(repo.Dir, name))
		if err != nil {
			t.Fatal(err)
		}
		*dst = data
	}
	return releaseverify.Verify(meta, releaseverify.Seen{}, now)
}

func canonicalAgain(t *testing.T, payload []byte) []byte {
	t.Helper()
	out, err := cjson.EncodeCanonical(json.RawMessage(payload))
	if err != nil {
		t.Fatal(err)
	}
	return out
}
