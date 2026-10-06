package install

import (
	"errors"
	"slices"
	"testing"

	joinhandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/join"
)

const testSigner = "0x1111111111111111111111111111111111111111"

// fakeTrust records what the install asks of the trust anchor, in order.
type fakeTrust struct {
	calls     []string
	signers   []string
	rotatedAt string
	expected  []string
	err       error
	// preflightErr fails the archive check before the join.
	preflightErr error
}

func (f *fakeTrust) SeedGenesisArchiveSigners() error {
	f.calls = append(f.calls, "seed")
	return f.err
}

func (f *fakeTrust) PreflightJoinArchive(expected []string) error {
	f.calls = append(f.calls, "preflight")
	f.expected = expected
	return f.preflightErr
}

func (f *fakeTrust) TrustJoinedArchiveSigners(signers []string, rotatedAt string, expected []string) error {
	f.calls = append(f.calls, "trust")
	f.signers, f.rotatedAt = signers, rotatedAt
	return f.err
}

func TestEstablishArchiveTrust_genesisSeedsFromTheOperatorWalletWithoutJoining(t *testing.T) {
	trust := &fakeTrust{}
	join, err := establishArchiveTrust(trust, false, nil, func() (*joinedCluster, error) {
		t.Fatal("a genesis install requested a join")
		return nil, nil
	})
	if err != nil || join != nil || !slices.Equal(trust.calls, []string{"seed"}) {
		t.Fatalf("join=%v err=%v calls=%v", join, err, trust.calls)
	}
}

func TestEstablishArchiveTrust_joinWritesTheClustersSignersFromTheResponse(t *testing.T) {
	trust := &fakeTrust{}
	var order []string
	resp := &joinhandlers.JoinResponse{ArchiveSigners: []string{testSigner}, ArchiveSignersRotatedAt: "2026-09-20T10:00:00Z"}
	join, err := establishArchiveTrust(trust, true, []string{testSigner}, func() (*joinedCluster, error) {
		order = append(order, "join")
		return &joinedCluster{resp: resp}, nil
	})
	if err != nil || join == nil || join.resp != resp {
		t.Fatalf("join=%v err=%v", join, err)
	}
	if !slices.Equal(trust.calls, []string{"preflight", "trust"}) || !slices.Equal(order, []string{"join"}) {
		t.Fatalf("the archive must be checked before the join, and the anchor written from its answer: %v, %v", trust.calls, order)
	}
	if !slices.Equal(trust.expected, []string{testSigner}) {
		t.Fatalf("the preflight did not get the expected signers: %v", trust.expected)
	}
	if !slices.Equal(trust.signers, []string{testSigner}) || trust.rotatedAt != "2026-09-20T10:00:00Z" {
		t.Fatalf("anchor written with %v at %q", trust.signers, trust.rotatedAt)
	}
}

func TestEstablishArchiveTrust_failuresStopTheInstall(t *testing.T) {
	if _, err := establishArchiveTrust(&fakeTrust{err: errors.New("no --operator-wallet")}, false, nil, nil); err == nil {
		t.Fatal("a genesis install continued without a trust anchor")
	}
	trust := &fakeTrust{}
	if _, err := establishArchiveTrust(trust, true, []string{testSigner}, func() (*joinedCluster, error) {
		return nil, errors.New("join rejected")
	}); err == nil || !slices.Equal(trust.calls, []string{"preflight"}) {
		t.Fatalf("a failed join wrote an anchor or continued: err=%v calls=%v", err, trust.calls)
	}
	trust = &fakeTrust{err: errors.New("sent no archive signers")}
	if _, err := establishArchiveTrust(trust, true, []string{testSigner}, func() (*joinedCluster, error) {
		return &joinedCluster{resp: &joinhandlers.JoinResponse{}}, nil
	}); err == nil {
		t.Fatal("a join whose response carried no signers continued")
	}
}

// An archive that cannot be installed must fail before the join spends the
// invite and writes a peer row.
func TestEstablishArchiveTrust_aBadArchiveNeverSpendsTheInvite(t *testing.T) {
	trust := &fakeTrust{preflightErr: errors.New("built for arm64")}
	_, err := establishArchiveTrust(trust, true, nil, func() (*joinedCluster, error) {
		t.Fatal("the join was requested for an archive that cannot be installed")
		return nil, nil
	})
	if err == nil {
		t.Fatal("a failed preflight let the install continue")
	}
}
