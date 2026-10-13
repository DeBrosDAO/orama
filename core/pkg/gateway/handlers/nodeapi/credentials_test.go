package nodeapi

import (
	"context"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/auth"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

func nodeKey(t *testing.T) *auth.NodeKeyPair {
	t.Helper()
	key, err := auth.NewNodeKeyPair()
	if err != nil {
		t.Fatalf("NewNodeKeyPair: %v", err)
	}
	return key
}

// nodeIdentity generates a libp2p identity and the peer id that carries its
// public key. Enrolment is checked against the key inside the id, so a test
// that made up a peer id could never enrol.
func nodeIdentity(t *testing.T) (string, crypto.PrivKey) {
	t.Helper()
	priv, pub, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateEd25519Key: %v", err)
	}
	id, err := peer.IDFromPublicKey(pub)
	if err != nil {
		t.Fatalf("IDFromPublicKey: %v", err)
	}
	return id.String(), priv
}

// stamped builds a request signed by whatever the caller holds.
func stamped(t *testing.T, signer auth.NodeStampSigner, nodeID string) (*http.Request, []byte) {
	t.Helper()
	body := []byte(`{}`)
	r := httptest.NewRequest(http.MethodPost, "/v1/internal/node/heartbeat", nil)
	r.RemoteAddr = loopbackCaller
	if err := auth.SignNodeAPI(signer, r, nodeID, body, time.Now()); err != nil {
		t.Fatalf("sign: %v", err)
	}
	return r, body
}

// verifies runs the whole resolution for one stamp, which is what the handler
// does — so these tests assert the rule, not the lookup.
func verifies(t *testing.T, db *recordingDB, signer auth.NodeStampSigner, nodeID string) bool {
	t.Helper()
	r, body := stamped(t, signer, nodeID)
	_, _, ok := auth.VerifyNodeAPI(NewCredentials(db).VerifierFor(context.Background()), r, body, time.Now())
	return ok
}

// A node is verified against the key it enrolled and nothing else.
func TestVerifierFor_anEnrolledNodeIsVerifiedByItsOwnKey(t *testing.T) {
	own := nodeKey(t)
	db := &recordingDB{credential: &credentialRow{PublicKey: own.PublicKey()}}

	if !verifies(t, db, own, testNodeID) {
		t.Error("an enrolled node's own key did not verify its stamp")
	}
}

// A node the cluster has no key for verifies against nothing — not against
// anything shared. This is the property that makes one machine's disk one
// machine: holding every secret the cluster distributes is not enough to speak
// as a node.
func TestVerifierFor_aNodeWithNoKeyVerifiesAgainstNothing(t *testing.T) {
	db := &recordingDB{neverEnrolled: true}

	if verifies(t, db, nodeKey(t), testNodeID) {
		t.Error("a node the cluster has no key for was verified by some key")
	}
}

// One node's key does not verify another's stamp, which is the whole point of
// per-node identity.
func TestVerifierFor_oneNodesKeyDoesNotSpeakForAnother(t *testing.T) {
	mine, theirs := nodeKey(t), nodeKey(t)
	db := &recordingDB{credential: &credentialRow{PublicKey: mine.PublicKey()}}

	if verifies(t, db, theirs, testNodeID) {
		t.Error("another node's key verified this node's stamp")
	}
}

// A retired machine's disk stops being a credential the moment it is retired —
// not when the cluster secret is next rotated, which in practice is never
// because it invalidates every token in the cluster at once.
func TestVerifierFor_aRevokedNodeVerifiesAgainstNothing(t *testing.T) {
	own := nodeKey(t)
	db := &recordingDB{credential: &credentialRow{
		PublicKey: own.PublicKey(),
		RevokedAt: "2026-09-05 12:00:00",
	}}

	if verifies(t, db, own, testNodeID) {
		t.Error("a revoked node still signed with its own key")
	}
}

// A recorded key that cannot be read verifies nothing, and says why. Reading it
// as "no row" would send a revoked node down the not-yet-enrolled path.
func TestVerifierFor_anUnreadableRecordIsRefusedAndReported(t *testing.T) {
	db := &recordingDB{credentialErr: errUnreadable}

	verifier, err := NewCredentials(db).VerifierFor(context.Background())(testNodeID)
	if verifier != nil {
		t.Error("a node was given a verifier while its recorded key could not be read")
	}
	if err == nil {
		t.Error("the lookup failure was swallowed; it reads in the logs as a forged stamp")
	}
}

// A stored key that is not a key verifies nothing, rather than producing a
// verifier that quietly accepts.
func TestVerifierFor_aCorruptRecordVerifiesNothing(t *testing.T) {
	db := &recordingDB{credential: &credentialRow{PublicKey: "not-a-key"}}
	if verifies(t, db, nodeKey(t), testNodeID) {
		t.Error("a node with an unreadable recorded key was verified by some key")
	}
}

// Enrolling records the key, once.
func TestEnrol_recordsAKeyTheFirstTime(t *testing.T) {
	db := &recordingDB{affected: 1, neverEnrolled: true}
	own := nodeKey(t)

	outcome, err := NewCredentials(db).Enrol(context.Background(), testNodeID, own.PublicKey())
	if err != nil {
		t.Fatalf("Enrol: %v", err)
	}
	if outcome != enrolRecorded {
		t.Errorf("outcome = %q, want %q", outcome, enrolRecorded)
	}
	if len(db.calls) != 1 {
		t.Fatalf("wrote %d times, want 1", len(db.calls))
	}
	if got := db.calls[0].args[0]; got != testNodeID {
		t.Errorf("recorded against %v, want %q", got, testNodeID)
	}
	if got := db.calls[0].args[1]; got != own.PublicKey() {
		t.Errorf("recorded %v, want the presented key", got)
	}
}

// A node re-asserts its key on every start. That is not a change and must not
// read as an attempted takeover — and it is the path every restart after the
// first takes, so getting it wrong would brick the fleet on its second boot.
func TestEnrol_theSameKeyAgainIsNotAChange(t *testing.T) {
	own := nodeKey(t)
	db := &recordingDB{credential: &credentialRow{PublicKey: own.PublicKey()}}

	outcome, err := NewCredentials(db).Enrol(context.Background(), testNodeID, own.PublicKey())
	if err != nil {
		t.Fatalf("Enrol: %v", err)
	}
	if outcome != enrolUnchanged {
		t.Errorf("outcome = %q, want %q", outcome, enrolUnchanged)
	}
	if len(db.calls) != 0 {
		t.Errorf("re-asserting the same key wrote %d rows", len(db.calls))
	}
}

// Replacing an enrolled node's key would be a takeover. Re-keying a rebuilt
// machine goes through the join, which needs an operator's invite.
func TestEnrol_refusesToReplaceALiveKey(t *testing.T) {
	db := &recordingDB{credential: &credentialRow{PublicKey: nodeKey(t).PublicKey()}}

	_, err := NewCredentials(db).Enrol(context.Background(), testNodeID, nodeKey(t).PublicKey())
	if err == nil {
		t.Fatal("an enrolled node's key was replaced")
	}
	if len(db.calls) != 0 {
		t.Errorf("a refused re-key wrote %d rows", len(db.calls))
	}
}

// A retired machine must not be able to re-admit itself — including by
// presenting the key it already had, which is what a machine that was simply
// switched back on would do. Refusing only a *different* key would let exactly
// that through, since re-asserting the same key is otherwise the normal case.
func TestEnrol_refusesARevokedNode(t *testing.T) {
	revoked := nodeKey(t)
	for name, presented := range map[string]string{
		"the key it already had": revoked.PublicKey(),
		"a new key":              nodeKey(t).PublicKey(),
	} {
		t.Run(name, func(t *testing.T) {
			db := &recordingDB{credential: &credentialRow{
				PublicKey: revoked.PublicKey(),
				RevokedAt: "2026-09-05 12:00:00",
			}}

			outcome, err := NewCredentials(db).Enrol(context.Background(), testNodeID, presented)
			if err == nil {
				t.Fatalf("a retired node re-admitted itself with %s (outcome %q)", name, outcome)
			}
			if len(db.calls) != 0 {
				t.Errorf("a revoked node wrote %d rows", len(db.calls))
			}
		})
	}
}

// The insert is conditional, so a second caller racing the first cannot replace
// the key that landed. The read above is not a lock: the table is replicated.
func TestEnrol_aRaceDoesNotReplaceTheKeyThatLanded(t *testing.T) {
	db := &recordingDB{affected: 0, neverEnrolled: true} // the conditional insert matched nothing

	_, err := NewCredentials(db).Enrol(context.Background(), testNodeID, nodeKey(t).PublicKey())
	if err == nil {
		t.Fatal("a caller that inserted nothing reported that it had recorded the key")
	}
	if len(db.calls) != 1 {
		t.Fatalf("wrote %d times, want 1", len(db.calls))
	}
	if !containsAll(db.calls[0].query, "INSERT INTO node_credentials", "WHERE NOT EXISTS") {
		t.Errorf("the insert is not conditional, so a race replaces the key that landed: %q", db.calls[0].query)
	}
}

// Something that is not a key is refused before anything is written.
func TestEnrol_refusesWhatIsNotAKey(t *testing.T) {
	db := &recordingDB{affected: 1, neverEnrolled: true}
	if _, err := NewCredentials(db).Enrol(context.Background(), testNodeID, "not-a-key"); err == nil {
		t.Error("something that is not a key was recorded")
	}
	if len(db.calls) != 0 {
		t.Errorf("a malformed key wrote %d rows", len(db.calls))
	}
}

// A lookup that fails is not "no row". Reading it that way would let a caller
// enrol over a live key whenever the database was unreachable.
func TestEnrol_anUnreadableRecordIsNotAnAbsentOne(t *testing.T) {
	db := &recordingDB{credentialErr: errUnreadable}

	if _, err := NewCredentials(db).Enrol(context.Background(), testNodeID, nodeKey(t).PublicKey()); err == nil {
		t.Error("a key was recorded while the existing record could not be read")
	}
	if len(db.calls) != 0 {
		t.Errorf("wrote %d rows despite an unreadable record", len(db.calls))
	}
}

func containsAll(haystack string, needles ...string) bool {
	for _, n := range needles {
		found := false
		for i := 0; i+len(n) <= len(haystack); i++ {
			if haystack[i:i+len(n)] == n {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
