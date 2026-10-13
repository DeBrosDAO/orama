package keeper_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/query"

	"github.com/DeBrosOfficial/network/chain/x/relay/keeper"
	"github.com/DeBrosOfficial/network/chain/x/relay/types"
)

// updateMsg is a MsgUpdateRelay for rk's node that gives it the RSA identity id, cross-signed by key and
// proved by id's own key.
func updateMsg(t *testing.T, rk relayKey, id rsaID, key ed25519.PrivateKey) *types.MsgUpdateRelay {
	t.Helper()
	return updateMsgExit(t, rk, id, key, false)
}

// updateMsgExit is updateMsg whose two proofs cover the exit declaration.
func updateMsgExit(t *testing.T, rk relayKey, id rsaID, key ed25519.PrivateKey, exit bool) *types.MsgUpdateRelay {
	t.Helper()
	return &types.MsgUpdateRelay{
		Exit:             exit,
		Operator:         rk.operator.String(),
		NodeId:           rk.nodeID,
		RsaFingerprint:   id.fp,
		Ed25519Signature: ed25519.Sign(key, types.CrossCertMessage(rk.nodeID, id.fp, exit)),
		RsaPublicKey:     id.der,
		RsaSignature:     id.proofExit(t, rk.nodeID, key.Public().(ed25519.PublicKey), exit),
	}
}

// eventsOf runs fn with a fresh event manager and returns the events of the given type it emitted.
func eventsOf(f *testFixture, eventType string, fn func(ctx sdk.Context)) []sdk.Event {
	ctx := f.Ctx.WithEventManager(sdk.NewEventManager())
	fn(ctx)
	var out []sdk.Event
	for _, e := range ctx.EventManager().Events() {
		if e.Type == eventType {
			out = append(out, e)
		}
	}
	return out
}

// hexOf is a fingerprint as the events write it: 40 upper-case hex digits.
func hexOf(fp []byte) string { return strings.ToUpper(hex.EncodeToString(fp)) }

func attr(e sdk.Event, key string) string {
	for _, a := range e.Attributes {
		if a.Key == key {
			return a.Value
		}
	}
	return ""
}

func TestRegisterRelay_refusals(t *testing.T) {
	f := newTestFixture(t)
	f.init(t, 1, []sdk.AccAddress{acc(1)}, nil)
	operator, other := acc(2), acc(3)
	rk := newRelayKey(t, "node-a", 0x11, operator, "10.1.0.1")
	f.register(t, rk, false)
	second := newRelayKey(t, "node-b", 0x22, operator, "10.1.0.2")
	f.Nodes.add(second)

	short := registerMsg(t, second)
	short.RsaFingerprint = short.RsaFingerprint[:types.RSAFingerprintLen-1]
	noNode := registerMsg(t, second)
	noNode.NodeId = ""
	sameIdentity := second
	sameIdentity.rsa, sameIdentity.fp = rk.rsa, rk.fp
	dup := registerMsg(t, sameIdentity)
	again := registerMsg(t, rk)
	secondIdentity := newRSAID(t, 0x66)
	again.RsaFingerprint, again.RsaPublicKey = secondIdentity.fp, secondIdentity.der
	again.RsaSignature = secondIdentity.proof(t, rk.nodeID, rk.pub)
	again.Ed25519Signature = ed25519.Sign(rk.priv, types.CrossCertMessage(rk.nodeID, again.RsaFingerprint, false))
	notOwner := registerMsg(t, second)
	notOwner.Operator = other.String()
	badSig := registerMsg(t, second)
	badSig.Ed25519Signature = ed25519.Sign(rk.priv, types.CrossCertMessage(second.nodeID, second.fp, false))
	unknown := registerMsg(t, second)
	unknown.NodeId = "no-such-node"

	for name, c := range map[string]struct {
		msg  *types.MsgRegisterRelay
		want string
	}{
		"fingerprint of 19 bytes":      {short, "rsa fingerprint must be 20 bytes"},
		"empty node id":                {noNode, "node id is empty"},
		"fingerprint already taken":    {dup, "fingerprint already registered"},
		"node already has a relay":     {again, "MsgUpdateRelay rotates"},
		"operator does not own node":   {notOwner, "does not own node"},
		"cross-signature of another":   {badSig, types.ErrCrossCertMismatch.Error()},
		"node the chain does not know": {unknown, "no-such-node"},
	} {
		_, err := f.Msg.RegisterRelay(f.Ctx, c.msg)
		require.Error(t, err, name)
		require.Contains(t, err.Error(), c.want, name)
	}
	relays, err := keeper.NewQueryServerImpl(f.Keeper).Relays(f.Ctx, &types.QueryRelaysRequest{})
	require.NoError(t, err)
	require.Len(t, relays.Relays, 1, "a refused registration leaves nothing behind")
}

func TestRegisterRelay_emitsWhatAnIndexerNeeds(t *testing.T) {
	f := newTestFixture(t)
	f.init(t, 1, []sdk.AccAddress{acc(1)}, nil)
	rk := newRelayKey(t, "node-a", 0xab, acc(2), "10.1.0.1")
	f.Nodes.add(rk)

	events := eventsOf(f, types.EventTypeRelayRegistered, func(ctx sdk.Context) {
		_, err := f.Msg.RegisterRelay(ctx, registerMsg(t, rk))
		require.NoError(t, err)
	})
	require.Len(t, events, 1)
	require.Equal(t, hexOf(rk.fp), attr(events[0], types.AttributeKeyRSAFingerprint), "40 upper-case hex digits")
	require.Len(t, attr(events[0], types.AttributeKeyRSAFingerprint), 2*types.RSAFingerprintLen)
	require.Equal(t, "node-a", attr(events[0], types.AttributeKeyNodeID))
	require.Equal(t, acc(2).String(), attr(events[0], types.AttributeKeyOperator))
	require.Equal(t, hex.EncodeToString(rk.pub), attr(events[0], types.AttributeKeyEd25519ID))
}

func TestUpdateRelay_rotatesTheFingerprintAndKeepsTheJail(t *testing.T) {
	f := newTestFixture(t)
	f.init(t, 1, []sdk.AccAddress{acc(1)}, nil)
	operator := acc(2)
	rk := newRelayKey(t, "node-a", 0x11, operator, "10.1.0.1")
	f.register(t, rk, false)
	require.NoError(t, f.Keeper.JailRelay(f.Ctx, rk.fp))

	rotated := newRSAID(t, 0x77)
	newFP := rotated.fp
	events := eventsOf(f, types.EventTypeRelayUpdated, func(ctx sdk.Context) {
		_, err := f.Msg.UpdateRelay(ctx, updateMsgExit(t, rk, rotated, rk.priv, true))
		require.NoError(t, err)
	})
	require.Len(t, events, 1)
	require.Equal(t, hexOf(newFP), attr(events[0], types.AttributeKeyRSAFingerprint))
	require.Equal(t, hexOf(rk.fp), attr(events[0], types.AttributeKeyPreviousRSAFingerprint))

	_, err := f.Keeper.Relays.Get(f.Ctx, rk.fp)
	require.Error(t, err, "the old fingerprint is no longer a registered relay")
	relay, err := f.Keeper.Relays.Get(f.Ctx, newFP)
	require.NoError(t, err)
	require.Equal(t, "node-a", relay.NodeId)
	require.True(t, relay.Exit)
	require.True(t, relay.Jailed, "rotating a key does not unjail a relay")
	indexed, err := f.Keeper.NodeIndex.Get(f.Ctx, "node-a")
	require.NoError(t, err)
	require.Equal(t, newFP, indexed)

	res, err := keeper.NewQueryServerImpl(f.Keeper).OperatorRelays(f.Ctx, &types.QueryOperatorRelaysRequest{Operator: operator.String()})
	require.NoError(t, err)
	require.Len(t, res.Relays, 1, "the operator's list follows the rotation")
	require.Equal(t, newFP, res.Relays[0].Relay.RsaFingerprint)
}

func TestUpdateRelay_followsTheNodesRotatedEd25519Key(t *testing.T) {
	f := newTestFixture(t)
	f.init(t, 1, []sdk.AccAddress{acc(1)}, nil)
	rk := newRelayKey(t, "node-a", 0x11, acc(2), "10.1.0.1")
	f.register(t, rk, false)

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	node := f.Nodes.byID["node-a"]
	node.pub = pub
	f.Nodes.byID["node-a"] = node

	stale := updateMsg(t, rk, rk.rsa, rk.priv)
	_, err = f.Msg.UpdateRelay(f.Ctx, stale)
	require.ErrorIs(t, err, types.ErrCrossCertMismatch, "the old key no longer cross-signs for the node")

	_, err = f.Msg.UpdateRelay(f.Ctx, updateMsg(t, rk, rk.rsa, priv))
	require.NoError(t, err)
	relay, err := f.Keeper.Relays.Get(f.Ctx, rk.fp)
	require.NoError(t, err)
	require.Equal(t, []byte(pub), relay.Ed25519Id, "the relay now carries the key the node binds")
}

func TestUpdateRelay_refusals(t *testing.T) {
	f := newTestFixture(t)
	reporter := acc(1)
	f.init(t, 1, []sdk.AccAddress{reporter}, nil)
	operator, other := acc(2), acc(3)
	rk := newRelayKey(t, "node-a", 0x11, operator, "10.1.0.1")
	f.register(t, rk, false)
	bare := newRelayKey(t, "node-b", 0x22, operator, "10.1.0.2")
	f.Nodes.add(bare)
	third := newRelayKey(t, "node-c", 0x33, other, "10.1.0.3")
	f.register(t, third, false)

	fresh := newRSAID(t, 0x44)
	notOwner := updateMsg(t, rk, fresh, rk.priv)
	notOwner.Operator = other.String()
	taken := updateMsg(t, rk, third.rsa, rk.priv)
	short := updateMsg(t, rk, fresh, rk.priv)
	short.RsaFingerprint = short.RsaFingerprint[:19]
	badSig := updateMsg(t, rk, fresh, third.priv)
	noRelay := updateMsg(t, bare, newRSAID(t, 0x55), bare.priv)
	asReporter := updateMsg(t, rk, fresh, rk.priv)
	asReporter.Operator = reporter.String()
	notTheKeysProof := updateMsg(t, rk, fresh, rk.priv)
	notTheKeysProof.RsaSignature = third.rsa.proof(t, rk.nodeID, rk.pub)

	for name, c := range map[string]struct {
		msg  *types.MsgUpdateRelay
		want string
	}{
		"operator does not own the node": {notOwner, "does not own node"},
		"fingerprint of another relay":   {taken, "fingerprint already registered"},
		"fingerprint of 19 bytes":        {short, "rsa fingerprint must be 20 bytes"},
		"cross-signature of another key": {badSig, types.ErrCrossCertMismatch.Error()},
		"a node with no relay":           {noRelay, types.ErrRelayNotRegistered.Error()},
		"a reporter":                     {asReporter, types.ErrReporterOperatesRelay.Error()},
		"an rsa proof by another key":    {notTheKeysProof, types.ErrRSAProofMismatch.Error()},
	} {
		_, err := f.Msg.UpdateRelay(f.Ctx, c.msg)
		require.Error(t, err, name)
		require.Contains(t, err.Error(), c.want, name)
	}
	relay, err := f.Keeper.Relays.Get(f.Ctx, rk.fp)
	require.NoError(t, err, "every refusal leaves the relay where it was")
	require.Equal(t, "node-a", relay.NodeId)
}

func TestReportEpoch_refusesARelayThatIsNotRegistered(t *testing.T) {
	f := newTestFixture(t)
	reporter := acc(1)
	f.init(t, 1, []sdk.AccAddress{reporter}, nil)
	rk := newRelayKey(t, "node-a", 0x11, acc(2), "10.1.0.1")
	f.register(t, rk, false)
	stranger := newRelayKey(t, "node-x", 0x99, acc(3), "10.9.0.1")

	err := f.submit(t, reporter, 1, []types.RelayObservation{obs(rk, 10, "1", false), obs(stranger, 10, "1", false)})
	require.ErrorContains(t, err, "relay is not registered", "one unregistered relay refuses the whole chunk, so a reporter reports only what the chain knows")

	rotated := newRSAID(t, 0x77)
	_, err = f.Msg.UpdateRelay(f.Ctx, updateMsg(t, rk, rotated, rk.priv))
	require.NoError(t, err)
	err = f.submitAt(t, reporter, 1, []types.RelayObservation{obs(rk, 10, "1", false)})
	require.ErrorContains(t, err, "relay is not registered", "a fingerprint a rotation replaced is not a registered relay")
}

func TestSettle_paysOnlyTheRegisteredFingerprintAfterARotation(t *testing.T) {
	f := newTestFixture(t)
	reporter, operator := acc(1), acc(2)
	f.init(t, 1, []sdk.AccAddress{reporter}, nil)
	rk := newRelayKey(t, "node-a", 0x11, operator, "10.1.0.1")
	f.register(t, rk, false)
	f.activate(t, []sdk.AccAddress{reporter}, []types.RelayObservation{obs(rk, 1, "1", false)})

	f.Emission.setCeiling(2, math.NewInt(1000))
	require.NoError(t, f.submit(t, reporter, 2, []types.RelayObservation{obs(rk, 40, "1", false)}))
	_, err := f.Msg.UpdateRelay(f.Ctx, updateMsg(t, rk, newRSAID(t, 0x77), rk.priv))
	require.NoError(t, err)

	result, err := f.Keeper.SettleEpoch(f.Ctx, 2)
	require.NoError(t, err)
	require.True(t, result.Minted.IsZero(), "the report named a fingerprint that is no longer registered")
	require.Empty(t, f.payouts(t, 2))
	require.True(t, f.Earnings.balance(operator).IsZero())
}

func TestSettle_emitsWhoWasPaidAndHowTheSplitFell(t *testing.T) {
	f := newTestFixture(t)
	reporter, operator := acc(1), acc(2)
	f.init(t, 1, []sdk.AccAddress{reporter}, nil)
	rk := newRelayKey(t, "node-a", 0x11, operator, "10.1.0.1")
	f.register(t, rk, false)
	f.activate(t, []sdk.AccAddress{reporter}, []types.RelayObservation{obs(rk, 1, "1", false)})
	f.Emission.setCeiling(2, math.NewInt(1000))
	require.NoError(t, f.submit(t, reporter, 2, []types.RelayObservation{obs(rk, 40, "1", false)}))

	ctx := f.Ctx.WithEventManager(sdk.NewEventManager())
	_, err := f.Keeper.SettleEpoch(ctx, 2)
	require.NoError(t, err)
	var payout, paid []sdk.Event
	for _, e := range ctx.EventManager().Events() {
		switch e.Type {
		case types.EventTypeRelayPayout:
			payout = append(payout, e)
		case types.EventTypeOperatorPaid:
			paid = append(paid, e)
		}
	}
	require.Len(t, payout, 1)
	require.Equal(t, "2", attr(payout[0], types.AttributeKeyEpoch))
	require.Equal(t, hexOf(rk.fp), attr(payout[0], types.AttributeKeyRSAFingerprint))
	require.Equal(t, "node-a", attr(payout[0], types.AttributeKeyNodeID))
	require.Equal(t, operator.String(), attr(payout[0], types.AttributeKeyOperator))
	require.Equal(t, "40", attr(payout[0], types.AttributeKeyAmount))
	require.Len(t, paid, 1)
	require.Equal(t, "40", attr(paid[0], types.AttributeKeyAmount))
	require.Equal(t, f.Earnings.balance(operator).String(), attr(paid[0], types.AttributeKeyEarnings), "the event names what reached the earnings account")
	earn, burn, archive := f.Earnings.balance(operator), f.Service.burned, f.Service.archive
	require.True(t, earn.Add(burn).Add(archive).Equal(math.NewInt(40)))
}

func TestOperatorRelays_listsEachRelayWithItsLastPayout(t *testing.T) {
	f := newTestFixture(t)
	reporter, operator, other := acc(1), acc(2), acc(3)
	f.init(t, 1, []sdk.AccAddress{reporter}, nil)
	paid := newRelayKey(t, "node-a", 0x11, operator, "10.1.0.1")
	idle := newRelayKey(t, "node-b", 0x22, operator, "10.2.0.1")
	foreign := newRelayKey(t, "node-c", 0x33, other, "10.3.0.1")
	f.register(t, paid, false)
	f.register(t, idle, false)
	f.register(t, foreign, false)
	f.activate(t, []sdk.AccAddress{reporter}, []types.RelayObservation{obs(paid, 1, "1", false)})
	for epoch, weight := range map[uint64]int64{2: 40, 3: 60} {
		f.Emission.setCeiling(epoch, math.NewInt(1000))
		require.NoError(t, f.submit(t, reporter, epoch, []types.RelayObservation{obs(paid, weight, "1", false), obs(foreign, 10, "1", false)}))
		_, err := f.Keeper.SettleEpoch(f.Ctx, epoch)
		require.NoError(t, err)
	}

	q := keeper.NewQueryServerImpl(f.Keeper)
	res, err := q.OperatorRelays(f.Ctx, &types.QueryOperatorRelaysRequest{Operator: operator.String()})
	require.NoError(t, err)
	require.Len(t, res.Relays, 2, "another operator's relay is not listed")
	byNode := map[string]types.RelayStanding{}
	for _, standing := range res.Relays {
		byNode[standing.Relay.NodeId] = standing
	}
	require.Equal(t, paid.fp, byNode["node-a"].Relay.RsaFingerprint)
	require.Equal(t, uint64(3), byNode["node-a"].LastPayoutEpoch)
	require.Equal(t, "60", byNode["node-a"].LastPayoutAmount.String())
	require.Equal(t, idle.fp, byNode["node-b"].Relay.RsaFingerprint)
	require.Equal(t, uint64(0), byNode["node-b"].LastPayoutEpoch)
	require.True(t, byNode["node-b"].LastPayoutAmount.IsZero())

	page, err := q.OperatorRelays(f.Ctx, &types.QueryOperatorRelaysRequest{Operator: operator.String(), Pagination: &query.PageRequest{Limit: 1}})
	require.NoError(t, err)
	require.Len(t, page.Relays, 1)
	require.NotNil(t, page.Pagination.NextKey, "a page of one leaves the other for the next page")
}

func TestOperatorRelays_refusalsAndEmpty(t *testing.T) {
	f := newTestFixture(t)
	q := keeper.NewQueryServerImpl(f.Keeper)
	_, err := q.OperatorRelays(f.Ctx, &types.QueryOperatorRelaysRequest{})
	require.Error(t, err)
	_, err = q.OperatorRelays(f.Ctx, &types.QueryOperatorRelaysRequest{Operator: "not-an-address"})
	require.Error(t, err)
	res, err := q.OperatorRelays(f.Ctx, &types.QueryOperatorRelaysRequest{Operator: acc(9).String()})
	require.NoError(t, err)
	require.NotNil(t, res.Relays)
	require.Empty(t, res.Relays)
}

func TestRelayPayouts_pagesOneEpochOnly(t *testing.T) {
	f := newTestFixture(t)
	reporter := acc(1)
	f.init(t, 1, []sdk.AccAddress{reporter}, nil)
	a := newRelayKey(t, "node-a", 0x11, acc(2), "10.1.0.1")
	b := newRelayKey(t, "node-b", 0x22, acc(3), "10.2.0.1")
	f.register(t, a, false)
	f.register(t, b, false)
	f.activate(t, []sdk.AccAddress{reporter}, []types.RelayObservation{obs(a, 1, "1", false)})
	for epoch := uint64(2); epoch <= 3; epoch++ {
		f.Emission.setCeiling(epoch, math.NewInt(1000))
		require.NoError(t, f.submit(t, reporter, epoch, []types.RelayObservation{obs(a, 10*int64(epoch), "1", false), obs(b, 5, "1", false)}))
		_, err := f.Keeper.SettleEpoch(f.Ctx, epoch)
		require.NoError(t, err)
	}

	q := keeper.NewQueryServerImpl(f.Keeper)
	res, err := q.RelayPayouts(f.Ctx, &types.QueryRelayPayoutsRequest{Epoch: 3})
	require.NoError(t, err)
	require.Len(t, res.Payouts, 2)
	for _, p := range res.Payouts {
		require.Equal(t, uint64(3), p.Epoch)
	}
	first, err := q.RelayPayouts(f.Ctx, &types.QueryRelayPayoutsRequest{Epoch: 2, Pagination: &query.PageRequest{Limit: 1}})
	require.NoError(t, err)
	require.Len(t, first.Payouts, 1)
	require.NotNil(t, first.Pagination.NextKey)

	none, err := q.RelayPayouts(f.Ctx, &types.QueryRelayPayoutsRequest{Epoch: 9})
	require.NoError(t, err)
	require.NotNil(t, none.Payouts)
	require.Empty(t, none.Payouts)
	_, err = q.RelayPayouts(f.Ctx, &types.QueryRelayPayoutsRequest{})
	require.Error(t, err, "epoch 0 is not an epoch")
}

func TestGenesis_rebuildsTheOperatorIndexAndTheLastPayouts(t *testing.T) {
	f := newTestFixture(t)
	reporter, operator := acc(1), acc(2)
	f.init(t, 1, []sdk.AccAddress{reporter}, nil)
	rk := newRelayKey(t, "node-a", 0x11, operator, "10.1.0.1")
	f.register(t, rk, false)
	f.activate(t, []sdk.AccAddress{reporter}, []types.RelayObservation{obs(rk, 1, "1", false)})
	for epoch, weight := range map[uint64]int64{2: 40, 3: 60} {
		f.Emission.setCeiling(epoch, math.NewInt(1000))
		require.NoError(t, f.submit(t, reporter, epoch, []types.RelayObservation{obs(rk, weight, "1", false)}))
		_, err := f.Keeper.SettleEpoch(f.Ctx, epoch)
		require.NoError(t, err)
	}
	exported, err := f.Keeper.ExportGenesis(f.Ctx)
	require.NoError(t, err)

	fresh := newTestFixture(t)
	require.NoError(t, fresh.Keeper.InitGenesis(fresh.Ctx, *exported))
	res, err := keeper.NewQueryServerImpl(fresh.Keeper).OperatorRelays(fresh.Ctx, &types.QueryOperatorRelaysRequest{Operator: operator.String()})
	require.NoError(t, err)
	require.Len(t, res.Relays, 1)
	require.Equal(t, uint64(3), res.Relays[0].LastPayoutEpoch, "the newest payout wins, whatever order genesis lists them in")
	require.Equal(t, "60", res.Relays[0].LastPayoutAmount.String())
}

// A report waits for its epoch to settle while its relay can be rotated: the state exported then must
// still be valid and import, and settlement skips the entry of the replaced fingerprint.
func TestGenesis_aRotationWithAReportWaitingStillExportsAndImports(t *testing.T) {
	f := newTestFixture(t)
	reporter, operator := acc(1), acc(2)
	f.init(t, 1, []sdk.AccAddress{reporter}, nil)
	rk := newRelayKey(t, "node-a", 0x11, operator, "10.1.0.1")
	f.register(t, rk, false)
	f.activate(t, []sdk.AccAddress{reporter}, []types.RelayObservation{obs(rk, 1, "1", false)})
	f.Emission.setCeiling(2, math.NewInt(1000))
	require.NoError(t, f.submit(t, reporter, 2, []types.RelayObservation{obs(rk, 40, "1", false)}))

	_, err := f.Msg.UpdateRelay(f.Ctx, updateMsg(t, rk, newRSAID(t, 0x77), rk.priv))
	require.NoError(t, err)
	exported, err := f.Keeper.ExportGenesis(f.Ctx)
	require.NoError(t, err)
	require.Len(t, exported.Reports, 1, "the report still waits")
	require.NoError(t, exported.Validate(), "an entry of a replaced fingerprint is not a reason to refuse the export")

	fresh := newTestFixture(t)
	require.NoError(t, fresh.Keeper.InitGenesis(fresh.Ctx, *exported))
}

// A fingerprint is public, so the chain asks the registrant to prove it holds the RSA key behind it:
// nobody can register, or rotate to, a fingerprint whose key they do not hold.
func TestRegisterRelay_refusesAFingerprintTheCallerCannotProve(t *testing.T) {
	f := newTestFixture(t)
	f.init(t, 1, []sdk.AccAddress{acc(1)}, nil)
	victim := newRelayKey(t, "node-victim", 0x11, acc(2), "10.1.0.1")
	squatter := newRelayKey(t, "node-squatter", 0x22, acc(3), "10.2.0.1")
	f.Nodes.add(squatter)

	withMsg := func(edit func(*types.MsgRegisterRelay)) error {
		msg := registerMsg(t, squatter)
		edit(msg)
		_, err := f.Msg.RegisterRelay(f.Ctx, msg)
		return err
	}
	victimFingerprint := func(msg *types.MsgRegisterRelay) {
		msg.RsaFingerprint = victim.fp
		msg.Ed25519Signature = ed25519.Sign(squatter.priv, types.CrossCertMessage(squatter.nodeID, victim.fp, false))
	}
	for name, edit := range map[string]func(*types.MsgRegisterRelay){
		"the victim's fingerprint with the squatter's own key": victimFingerprint,
		"the victim's fingerprint, its public key and the squatter's signature": func(msg *types.MsgRegisterRelay) {
			victimFingerprint(msg)
			msg.RsaPublicKey = victim.rsa.der
		},
		"a proof made for another node": func(msg *types.MsgRegisterRelay) {
			msg.RsaSignature = squatter.rsa.proof(t, "node-victim", squatter.pub)
		},
		"a proof made for another relay key": func(msg *types.MsgRegisterRelay) {
			otherPub, _, err := ed25519.GenerateKey(rand.Reader)
			require.NoError(t, err)
			msg.RsaSignature = squatter.rsa.proof(t, squatter.nodeID, otherPub)
		},
		"no key and no signature": func(msg *types.MsgRegisterRelay) {
			msg.RsaPublicKey, msg.RsaSignature = nil, nil
		},
		"a key of 2000 bytes": func(msg *types.MsgRegisterRelay) {
			msg.RsaPublicKey = make([]byte, 2*types.MaxRSAPublicKeyLen)
		},
		"a key with a byte appended": func(msg *types.MsgRegisterRelay) {
			msg.RsaPublicKey = append(append([]byte(nil), msg.RsaPublicKey...), 0)
		},
		"a cut signature": func(msg *types.MsgRegisterRelay) {
			msg.RsaSignature = msg.RsaSignature[:len(msg.RsaSignature)-1]
		},
	} {
		err := withMsg(edit)
		require.ErrorIs(t, err, types.ErrRSAProofMismatch, name)
	}
	require.NoError(t, withMsg(func(*types.MsgRegisterRelay) {}), "the squatter's own fingerprint with its own proof registers")
	_, err := f.Keeper.Relays.Get(f.Ctx, victim.fp)
	require.Error(t, err, "the victim's fingerprint stayed free")

	f.Nodes.add(victim)
	_, err = f.Msg.RegisterRelay(f.Ctx, registerMsg(t, victim))
	require.NoError(t, err, "the victim registers its own fingerprint, which nobody could take before it")
}

// The exit declaration is covered by both proofs: proofs made for a non-exit relay cannot register or
// update it as an exit, and an exit relay's proofs cannot declare it a plain relay.
func TestRegisterRelay_exitIsBoundIntoBothProofs(t *testing.T) {
	f := newTestFixture(t)
	f.init(t, 1, []sdk.AccAddress{acc(1)}, nil)
	rk := newRelayKey(t, "node-a", 0x11, acc(2), "10.1.0.1")
	f.Nodes.add(rk)

	for _, exit := range []bool{false, true} {
		replayed := registerMsgExit(t, rk, exit)
		replayed.Exit = !exit
		_, err := f.Msg.RegisterRelay(f.Ctx, replayed)
		require.ErrorIs(t, err, types.ErrCrossCertMismatch, "both proofs replayed with the opposite exit, exit=%v", exit)

		onlyCert := registerMsgExit(t, rk, !exit)
		onlyCert.Exit = !exit
		onlyCert.RsaSignature = rk.rsa.proofExit(t, rk.nodeID, rk.pub, exit)
		_, err = f.Msg.RegisterRelay(f.Ctx, onlyCert)
		require.ErrorIs(t, err, types.ErrRSAProofMismatch, "the cross-cert covers the exit but the RSA proof does not, exit=%v", exit)
	}
	_, err := f.Msg.RegisterRelay(f.Ctx, registerMsgExit(t, rk, true))
	require.NoError(t, err, "proofs made for an exit register an exit")
	relay, err := f.Keeper.Relays.Get(f.Ctx, rk.fp)
	require.NoError(t, err)
	require.True(t, relay.Exit)
}

func TestUpdateRelay_exitIsBoundIntoBothProofs(t *testing.T) {
	f := newTestFixture(t)
	f.init(t, 1, []sdk.AccAddress{acc(1)}, nil)
	rk := newRelayKey(t, "node-a", 0x11, acc(2), "10.1.0.1")
	f.register(t, rk, false)

	promoted := updateMsgExit(t, rk, rk.rsa, rk.priv, false)
	promoted.Exit = true
	_, err := f.Msg.UpdateRelay(f.Ctx, promoted)
	require.ErrorIs(t, err, types.ErrCrossCertMismatch, "the proofs of a non-exit relay replayed as an exit")

	halfSigned := updateMsgExit(t, rk, rk.rsa, rk.priv, true)
	halfSigned.RsaSignature = rk.rsa.proofExit(t, rk.nodeID, rk.pub, false)
	_, err = f.Msg.UpdateRelay(f.Ctx, halfSigned)
	require.ErrorIs(t, err, types.ErrRSAProofMismatch, "a fresh cross-cert with the old RSA proof")

	_, err = f.Msg.UpdateRelay(f.Ctx, updateMsgExit(t, rk, rk.rsa, rk.priv, true))
	require.NoError(t, err, "fresh proofs over the new exit value update it")
	relay, err := f.Keeper.Relays.Get(f.Ctx, rk.fp)
	require.NoError(t, err)
	require.True(t, relay.Exit)

	demoted := updateMsgExit(t, rk, rk.rsa, rk.priv, true)
	demoted.Exit = false
	_, err = f.Msg.UpdateRelay(f.Ctx, demoted)
	require.ErrorIs(t, err, types.ErrCrossCertMismatch, "an exit relay's proofs replayed as a plain relay")
}
