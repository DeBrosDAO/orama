package indexer

import (
	"bytes"
	"context"
	"fmt"
	"strconv"

	cosmath "cosmossdk.io/math"
	abci "github.com/cometbft/cometbft/abci/types"
	cmttypes "github.com/cometbft/cometbft/types"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/types/bech32"
	slashingtypes "github.com/cosmos/cosmos-sdk/x/slashing/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
)

const (
	queryValidator   = "/cosmos.staking.v1beta1.Query/Validator"
	eventSlash       = "slash"
	msgUnjailTypeURL = "/cosmos.slashing.v1beta1.MsgUnjail"
)

// signerSet is the validator set of one height, in the order its commit's signatures use.
type signerSet struct {
	hash  []byte
	addrs [][]byte
}

// foldSigning counts, for each validator, the commit the block carries. The commit is the votes
// for the block before it, ordered by the validator set of that block; an absent vote names no
// validator, so the set (read from the node when its hash changes) says whose it was. A vote that
// is not a commit vote (absent or nil) is a missed block.
func (f *Follower) foldSigning(ctx context.Context, w *writer, wt watch, b blockFacts) error {
	if b.commit == nil || len(b.commit.Signatures) == 0 || b.height < wt.StartHeight {
		return nil
	}
	set, err := f.signers(ctx, wt, b.height-1)
	if err != nil {
		return err
	}
	if len(set) != len(b.commit.Signatures) {
		return fmt.Errorf("block %d carries %d votes but the validator set of height %d has %d members", b.height, len(b.commit.Signatures), b.height-1, len(set))
	}
	for i, sig := range b.commit.Signatures {
		if len(sig.ValidatorAddress) > 0 && !bytes.Equal(sig.ValidatorAddress, set[i]) {
			return fmt.Errorf("vote %d of block %d is by %x but the validator set of height %d has %x there", i, b.height, sig.ValidatorAddress, b.height-1, set[i])
		}
		c, err := w.counters(wt.Epoch, set[i])
		if err != nil {
			return err
		}
		if sig.BlockIDFlag == cmttypes.BlockIDFlagCommit {
			c.Signed++
		} else {
			c.Missed++
		}
		if err := w.putCounters(wt.Epoch, set[i], c); err != nil {
			return err
		}
	}
	return nil
}

// signers returns the validator set of the height whose votes the block carries. The set is kept
// from one block to the next and read again only when its hash (taken from the block header it
// signed) differs, so a stable set costs no request.
func (f *Follower) signers(ctx context.Context, wt watch, height int64) ([][]byte, error) {
	if len(wt.ValSetHash) > 0 && bytes.Equal(f.signing.hash, wt.ValSetHash) {
		return f.signing.addrs, nil
	}
	addrs, err := f.chain.ValidatorAddresses(ctx, height)
	if err != nil {
		return nil, err
	}
	f.signing = signerSet{hash: wt.ValSetHash, addrs: addrs}
	return addrs, nil
}

// foldEvents records the slashes and jailings x/slashing announced in the block's begin- and
// end-block events. A slash event names the validator by consensus address with the power it had,
// the reason and the norama burned; a jailing is a "slash" event with a "jailed" attribute alone.
func (f *Follower) foldEvents(w *writer, wt watch, b blockFacts) error {
	reasons := map[string]string{}
	for i, e := range b.events {
		if e.Type != eventSlash {
			continue
		}
		if err := f.foldSlashEvent(w, wt, b, uint32(i), e, reasons); err != nil {
			return fmt.Errorf("slash event %d of block %d: %w", i, b.height, err)
		}
	}
	return nil
}

// foldSlashEvent records what one slash event announces. A downtime event carries both a slash and
// the jailing in one event; a double-sign slash and its jailing are two events.
func (f *Follower) foldSlashEvent(w *writer, wt watch, b blockFacts, idx uint32, e abci.Event, reasons map[string]string) error {
	if address := attr(e, "address"); address != "" {
		reasons[address] = attr(e, "reason")
		if err := f.recordSlash(w, wt, b, idx, e); err != nil {
			return err
		}
	}
	if jailed := attr(e, "jailed"); jailed != "" {
		return f.recordJail(w, b, jailed, reasons[jailed])
	}
	return nil
}

func (f *Follower) recordSlash(w *writer, wt watch, b blockFacts, idx uint32, e abci.Event) error {
	cons, err := decodeCons(attr(e, "address"))
	if err != nil {
		return err
	}
	power, err := strconv.ParseInt(attr(e, "power"), 10, 64)
	if err != nil {
		return fmt.Errorf("power %q is not an integer: %w", attr(e, "power"), err)
	}
	burned, ok := cosmath.NewIntFromString(attr(e, "burned_coins"))
	if !ok {
		return fmt.Errorf("burned_coins %q is not an integer", attr(e, "burned_coins"))
	}
	s := Slash{Height: b.height, Time: b.time.UTC(), Epoch: wt.Epoch, Reason: attr(e, "reason"), Power: power, Burned: burned.String()}
	if err := w.setJSON(slashKey(cons, b.height, idx), s); err != nil {
		return err
	}
	c, err := w.counters(wt.Epoch, cons)
	if err != nil {
		return err
	}
	c.Slashes++
	c.Burned = c.Burned.Add(burned)
	return w.putCounters(wt.Epoch, cons, c)
}

// recordJail opens a jail period unless the validator's latest one is still open.
func (f *Follower) recordJail(w *writer, b blockFacts, consAddr, reason string) error {
	cons, err := decodeCons(consAddr)
	if err != nil {
		return err
	}
	t := b.time.UTC()
	return w.openJail(cons, b.height, JailPeriod{JailedHeight: b.height, JailedTime: &t, Reason: reason})
}

// openJail stores a new jail period at key height, unless the latest period is still open. The
// exception is a period opened from the state after the index's first block, whose start is not
// known: that block's own jailing event says when it began, and replaces it.
func (w *writer) openJail(cons []byte, height int64, p JailPeriod) error {
	key, last, found, err := w.latestJail(cons)
	if err != nil {
		return err
	}
	if found && last.UnjailedHeight == 0 {
		learned := last.JailedHeight == 0 && p.JailedHeight == height && bytes.Equal(key, jailKey(cons, height))
		if !learned {
			return nil
		}
	}
	return w.setJSON(jailKey(cons, height), p)
}

// noteUnjails closes the jail period of each validator a successful transaction unjails.
func (f *Follower) noteUnjails(ctx context.Context, w *writer, pos txPos, msgs []*codectypes.Any) error {
	for _, m := range msgs {
		if m.TypeUrl != msgUnjailTypeURL {
			continue
		}
		var msg slashingtypes.MsgUnjail
		if err := f.codec.Unmarshal(m.Value, &msg); err != nil {
			return fmt.Errorf("failed to read an unjail message: %w", err)
		}
		cons, err := f.operatorConsensus(ctx, w, pos.height, msg.ValidatorAddr)
		if err != nil {
			return err
		}
		if err := w.closeJail(cons, pos); err != nil {
			return err
		}
	}
	return nil
}

// operatorConsensus resolves an operator to its consensus address: from the registry, or, for a
// validator created since the last epoch closed, from the chain's state after the block.
func (f *Follower) operatorConsensus(ctx context.Context, w *writer, height int64, operator string) ([]byte, error) {
	cons, ok, err := w.consensusOf(operator)
	if err != nil || ok {
		return cons, err
	}
	var resp stakingtypes.QueryValidatorResponse
	r := chainReader{ctx: ctx, chain: f.chain, height: height}
	if err := r.ask(queryValidator, &stakingtypes.QueryValidatorRequest{ValidatorAddr: operator}, &resp); err != nil {
		return nil, err
	}
	ref, err := f.validatorRef(r, resp.Validator, 0)
	if err != nil {
		return nil, err
	}
	return ref.cons, nil
}

// closeJail ends the validator's open jail period, or records an unjail whose jailing the index did
// not see.
func (w *writer) closeJail(cons []byte, pos txPos) error {
	key, last, found, err := w.latestJail(cons)
	if err != nil {
		return err
	}
	t := pos.time.UTC()
	if !found || last.UnjailedHeight != 0 {
		key, last = jailKey(cons, pos.height), JailPeriod{}
	}
	last.UnjailedHeight, last.UnjailedTime = pos.height, &t
	return w.setJSON(key, last)
}

// decodeCons reads a bech32 consensus address.
func decodeCons(s string) ([]byte, error) {
	hrp, raw, err := bech32.DecodeAndConvert(s)
	if err != nil {
		return nil, fmt.Errorf("consensus address %q: %w", s, err)
	}
	if hrp != params.Bech32PrefixConsAddr || len(raw) != consLen {
		return nil, fmt.Errorf("%q is not a consensus address", s)
	}
	return raw, nil
}
