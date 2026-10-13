package policy

import (
	"testing"

	"cosmossdk.io/math"
)

func TestCheckTargetRejectsEveryForeignAndPlainPath(t *testing.T) {
	amount := math.NewInt(1)
	cap := math.NewInt(MaxFeeTopup)
	kinds := []Target{
		TargetPlainUser,
		TargetContract,
		TargetOwnBond,
		TargetOwnNodeBond,
		TargetOwnDeposit,
		TargetFeeEarnings,
	}
	for _, kind := range kinds {
		if err := CheckTarget(kind, "orama1alice", "orama1bob", amount, cap); err == nil {
			t.Fatalf("kind %d allowed a foreign target", kind)
		}
		if kind == TargetPlainUser {
			if err := CheckTarget(kind, "orama1alice", "orama1alice", amount, cap); err == nil {
				t.Fatal("plain account allowed for the owner")
			}
			continue
		}
		if err := CheckTarget(kind, "orama1alice", "orama1alice", amount, cap); err != nil {
			t.Fatalf("kind %d: owner rejected: %v", kind, err)
		}
	}
}

func TestFeeTopupCap(t *testing.T) {
	cap := math.NewInt(MaxFeeTopup)
	if err := CheckTarget(TargetFeeEarnings, "orama1a", "orama1a", cap, cap); err != nil {
		t.Fatal(err)
	}
	if err := CheckTarget(TargetFeeEarnings, "orama1a", "orama1a", cap.AddRaw(1), cap); err != ErrFeeTopupCap {
		t.Fatalf("got %v", err)
	}
}

func TestZeroAmountRejected(t *testing.T) {
	if err := CheckTarget(TargetOwnBond, "orama1a", "orama1a", math.ZeroInt(), math.NewInt(1)); err != ErrAmount {
		t.Fatalf("got %v", err)
	}
}
