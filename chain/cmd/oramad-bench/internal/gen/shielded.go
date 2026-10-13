package gen

import (
	"context"
	"encoding/hex"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/shielded"
	shieldedtypes "github.com/DeBrosOfficial/network/chain/x/shielded/types"
)

// Names of the shielded transaction types. A transfer type carries its action count.
const (
	Shield                 = "shield"
	Unshield               = "unshield"
	ShieldedTransferPrefix = "shielded-transfer-"
)

// TransferActionCounts are the sizes of shielded transfer the generator can send. The chain's
// limit is x/shielded's max_actions_per_bundle (16 at genesis).
var TransferActionCounts = []int{2, 4, 8, 16}

// TransferActions returns the action count of a shielded transfer type name.
func TransferActions(name string) (int, bool) {
	for _, n := range TransferActionCounts {
		if name == fmt.Sprintf("%s%d", ShieldedTransferPrefix, n) {
			return n, true
		}
	}
	return 0, false
}

func init() {
	register(Shield, func() Generator {
		return &shieldedType{name: Shield, kind: shielded.EntryShield, what: "x/shielded MsgShield of one note, funded from the signer's bank balance"}
	})
	register(Unshield, func() Generator {
		return &shieldedType{name: Unshield, kind: shielded.EntryUnshield, what: "x/shielded MsgUnshield of a note into the signer's fee-only balance"}
	})
	for _, n := range TransferActionCounts {
		n := n
		name := fmt.Sprintf("%s%d", ShieldedTransferPrefix, n)
		register(name, func() Generator {
			return &shieldedType{
				name: name, kind: shielded.EntryTransfer, actions: n,
				what: fmt.Sprintf("x/shielded MsgShieldedTransfer of %d actions: a signature-less, fee-less private transfer that spends real notes", n),
			}
		})
	}
}

// shieldedType sends bundles from the cache. Proving is slow, so the bundles are made before the
// run (`oramad-bench prepare`); the type only sends them, each once.
type shieldedType struct {
	name      string
	kind      string
	actions   int
	what      string
	actionGas uint64
	entries   []shielded.Entry
	accounts  []string
	// planned is how many entries Plan has handed out (one past the highest k).
	planned int
}

func (s *shieldedType) Name() string     { return s.name }
func (s *shieldedType) Describe() string { return s.what }

func (s *shieldedType) Prepare(ctx context.Context, env *Env, total int) error {
	if env.Shielded == nil {
		return Skipf("there is no shielded cache; run `oramad-bench prepare` first (it needs a localnet started with WITH_SHIELDED=1)")
	}
	s.entries = env.Shielded.Peek(s.kind, s.actions, total)
	if len(s.entries) == 0 {
		return Skipf("the shielded cache holds no unused %s bundle of %s", s.kind, actionsLabel(s.actions))
	}
	var params shieldedtypes.QueryParamsResponse
	if err := env.Chain.Client.Query(ctx, "/orama.shielded.v1.Query/Params", &shieldedtypes.QueryParamsRequest{}, &params); err != nil {
		return fmt.Errorf("read the shielded params: %w", err)
	}
	s.actionGas = params.Params.ActionGas
	s.accounts = make([]string, len(env.Accounts))
	for i, a := range env.Accounts {
		s.accounts[i] = a.Address
	}
	return nil
}

func actionsLabel(n int) string {
	if n == 0 {
		return "any size"
	}
	return fmt.Sprintf("%d actions", n)
}

func (s *shieldedType) Available() int { return len(s.entries) }

// Consumed lists the cache entries Plan has handed out so far, for the runner to mark used.
func (s *shieldedType) Consumed() []string {
	ids := make([]string, s.planned)
	for i := range ids {
		ids[i] = s.entries[i].ID
	}
	return ids
}

func (s *shieldedType) Plan(k int) (Planned, error) {
	if k < 0 || k >= len(s.entries) {
		return Planned{}, fmt.Errorf("%s: bundle %d requested, the cache has %d", s.name, k, len(s.entries))
	}
	e := s.entries[k]
	s.planned = max(s.planned, k+1)
	raw, err := hex.DecodeString(e.Bundle)
	if err != nil {
		return Planned{}, fmt.Errorf("cache entry %s is not hex: %w", e.ID, err)
	}
	return s.message(e, raw)
}

func (s *shieldedType) message(e shielded.Entry, raw []byte) (Planned, error) {
	if e.Kind == shielded.EntryTransfer {
		msg := &shieldedtypes.MsgShieldedTransfer{Signer: shieldedtypes.SignerlessAddress().String(), Bundle: raw}
		return Planned{Account: Signerless, Msg: msg, Gas: uint64(e.Actions) * s.actionGas}, nil
	}
	if e.Account < 0 || e.Account >= len(s.accounts) {
		return Planned{}, fmt.Errorf("cache entry %s is for bench account %d, the run has %d", e.ID, e.Account, len(s.accounts))
	}
	var msg sdk.Msg = &shieldedtypes.MsgShield{Signer: s.accounts[e.Account], Bundle: raw}
	if e.Kind == shielded.EntryUnshield {
		msg = &shieldedtypes.MsgUnshield{Signer: s.accounts[e.Account], Bundle: raw, Target: shieldedtypes.UnshieldTargetFeeTopup}
	}
	return Planned{Account: e.Account, Msg: msg}, nil
}
