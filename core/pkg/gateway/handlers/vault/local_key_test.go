package vault

import (
	"bytes"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/shamir"
)

func TestThresholdsForGuardianCount(t *testing.T) {
	tests := []struct {
		n, k, w int
	}{
		{1, 1, 1},
		{2, 2, 2},
		{3, 2, 3},
		{5, 2, 4},
		{9, 3, 6},
	}
	for _, tt := range tests {
		k, w := thresholdsForGuardianCount(tt.n)
		if k != tt.k || w != tt.w {
			t.Errorf("n=%d: K,W = %d,%d want %d,%d", tt.n, k, w, tt.k, tt.w)
		}
	}
	if got := shamir.AdaptiveThreshold(1); got != 2 {
		t.Errorf("AdaptiveThreshold(1) = %d, want 2 — the Shamir floor must not move", got)
	}
}

func TestSplitEnvelope_localKeyN1(t *testing.T) {
	envelope := []byte("eval-ciphertext")
	shares, k, w, err := splitEnvelope(envelope, 1)
	if err != nil {
		t.Fatalf("splitEnvelope n=1: %v", err)
	}
	if k != 1 || w != 1 {
		t.Errorf("n=1 K,W = %d,%d want 1,1", k, w)
	}
	if len(shares) != 1 {
		t.Fatalf("n=1 shares = %d, want 1", len(shares))
	}
	if shares[0].X != 1 {
		t.Errorf("share X = %d, want 1", shares[0].X)
	}
	if !bytes.Equal(shares[0].Y, envelope) {
		t.Errorf("share Y = %q, want the envelope", shares[0].Y)
	}

	got, err := combineEnvelope(shares, 1)
	if err != nil {
		t.Fatalf("combineEnvelope k=1: %v", err)
	}
	if !bytes.Equal(got, envelope) {
		t.Errorf("round-trip = %q, want %q", got, envelope)
	}
}

func TestSplitEnvelope_n3StillShamir(t *testing.T) {
	envelope := []byte("production-ciphertext-ok")
	shares, k, w, err := splitEnvelope(envelope, 3)
	if err != nil {
		t.Fatalf("splitEnvelope n=3: %v", err)
	}
	if k != 2 || w != 3 {
		t.Errorf("n=3 K,W = %d,%d want 2,3", k, w)
	}
	if len(shares) != 3 {
		t.Fatalf("n=3 shares = %d, want 3", len(shares))
	}
	got, err := combineEnvelope(shares[:k], k)
	if err != nil {
		t.Fatalf("combine n=3: %v", err)
	}
	if !bytes.Equal(got, envelope) {
		t.Errorf("n=3 round-trip = %q, want %q", got, envelope)
	}
	if bytes.Equal(shares[0].Y, envelope) {
		t.Error("n=3 share 0 Y is the envelope — Shamir was skipped")
	}
}

func TestCombineEnvelope_storedK1SurvivesFleetGrowth(t *testing.T) {
	envelope := []byte("written-on-one-guardian")
	shares, _, _, err := splitEnvelope(envelope, 1)
	if err != nil {
		t.Fatal(err)
	}
	// Discovery later returns 3 guardians; pull still uses stored threshold 1.
	got, err := combineEnvelope(shares, 1)
	if err != nil {
		t.Fatalf("stored K=1 after growth: %v", err)
	}
	if !bytes.Equal(got, envelope) {
		t.Errorf("got %q, want %q", got, envelope)
	}
}

func TestSplitEnvelope_n2Unchanged(t *testing.T) {
	envelope := []byte("two-guardian-cluster")
	shares, k, w, err := splitEnvelope(envelope, 2)
	if err != nil {
		t.Fatalf("n=2: %v", err)
	}
	if k != 2 || w != 2 {
		t.Errorf("n=2 K,W = %d,%d want 2,2", k, w)
	}
	got, err := combineEnvelope(shares, k)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, envelope) {
		t.Errorf("n=2 round-trip = %q", got)
	}
}
