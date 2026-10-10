package indexer

import (
	"encoding/binary"
	"fmt"
	"strings"
	"testing"
	"time"

	"cosmossdk.io/math"
	cmttypes "github.com/cometbft/cometbft/types"
	gogoproto "github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/require"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/bech32"
	"github.com/cosmos/cosmos-sdk/types/query"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	emissiontypes "github.com/DeBrosOfficial/network/chain/x/emission/types"
	feestypes "github.com/DeBrosOfficial/network/chain/x/fees/types"
	powertypes "github.com/DeBrosOfficial/network/chain/x/power/types"
)

// fakeValidator is a staking validator of the fake chain.
type fakeValidator struct {
	operator string
	key      *ed25519.PrivKey
	status   stakingtypes.BondStatus
	jailed   bool
	power    int64
}

func (v fakeValidator) cons() []byte { return v.key.PubKey().Address().Bytes() }

func (v fakeValidator) staking() stakingtypes.Validator {
	pk, err := codectypes.NewAnyWithValue(v.key.PubKey())
	if err != nil {
		panic(err)
	}
	return stakingtypes.Validator{
		OperatorAddress: v.operator, ConsensusPubkey: pk, Jailed: v.jailed, Status: v.status,
		Tokens: math.NewInt(v.power * 1000), Description: stakingtypes.Description{Moniker: "moniker-" + v.operator[len(v.operator)-4:]},
	}
}

// newFakeValidator is a bonded validator whose keys and operator address derive from seed.
func newFakeValidator(t *testing.T, seed byte, power int64) fakeValidator {
	t.Helper()
	raw := make([]byte, 20)
	raw[18], raw[19] = 0xAA, seed
	op, err := bech32.ConvertAndEncode(params.Bech32PrefixValAddr, raw)
	require.NoError(t, err)
	return fakeValidator{operator: op, key: ed25519.GenPrivKeyFromSecret([]byte{seed}), status: stakingtypes.Bonded, power: power}
}

// fakeLedger is the chain's economy as the aggregates query it. It replays x/emission's closing rule
// over the fake chain's block times, so an epoch closes where the real chain would close it, and
// every running total is a fixed amount per block or per closed epoch.
type fakeLedger struct {
	durationSec  int64
	minBlocks    uint64
	mintPerEpoch int64
	devPerEpoch  int64
	svcPerEpoch  int64
	burnPerBlock int64
	feesPerBlock int64
	// balances are what the module accounts hold, by module name.
	balances map[string]int64
	// validators are the staking validators.
	validators []fakeValidator
	// stuck makes the chain never close an epoch, whatever the rule says.
	stuck bool
}

// newFakeLedger is a chain whose epoch never closes and that has no validators.
func newFakeLedger() *fakeLedger {
	return &fakeLedger{durationSec: 1 << 40, minBlocks: 1 << 40, balances: map[string]int64{}}
}

// epochs closes an epoch every `blocks` blocks (a block is a second).
func (l *fakeLedger) epochs(blocks int64) *fakeLedger {
	l.durationSec, l.minBlocks = blocks, uint64(blocks)
	return l
}

func (l *fakeLedger) state(h int64) emissiontypes.EpochState {
	epoch, start, blocks, closed := uint64(1), blockTime(0).UnixNano(), uint64(0), int64(0)
	for i := int64(1); i <= h; i++ {
		blocks++
		if !l.stuck && blockTime(i).Sub(time.Unix(0, start)) >= time.Duration(l.durationSec)*time.Second && blocks >= l.minBlocks {
			epoch, start, blocks = epoch+1, blockTime(i).UnixNano(), 0
			closed++
		}
	}
	return emissiontypes.EpochState{
		CurrentEpoch: epoch, EpochStartUnixNano: start, BlocksInEpoch: blocks,
		CumulativeMinted:            math.NewInt(l.mintPerEpoch * closed),
		CumulativeDevelopmentMinted: math.NewInt(l.devPerEpoch * closed),
		CumulativeServiceMinted:     math.NewInt(l.svcPerEpoch * closed),
		CumulativeBurned:            math.NewInt(l.burnPerBlock * h),
		CumulativeFaucetMinted:      math.ZeroInt(), GenesisSupply: math.ZeroInt(),
		ValidatorSplitDelta: math.ZeroInt(), FaucetEpochMinted: math.ZeroInt(),
	}
}

func (l *fakeLedger) supply(h int64) int64 {
	s := l.state(h)
	minted := s.CumulativeMinted.Add(s.CumulativeDevelopmentMinted).Add(s.CumulativeServiceMinted)
	return minted.Sub(s.CumulativeBurned).Int64()
}

func (l *fakeLedger) answer(method string, height int64, req gogoproto.Message) (gogoproto.Message, error) {
	switch method {
	case queryEmissionState:
		return &emissiontypes.QueryCurrentEpochResponse{EpochState: l.state(height)}, nil
	case queryEmissionParams:
		return &emissiontypes.QueryParamsResponse{Params: emissiontypes.Params{
			EpochDurationSeconds: l.durationSec, MinBlocksPerEpoch: l.minBlocks,
			FaucetMaxDrip: math.ZeroInt(), FaucetEpochCap: math.ZeroInt(),
		}}, nil
	case queryEmissionSchedule:
		return &emissiontypes.QueryScheduleAtResponse{Epoch: req.(*emissiontypes.QueryScheduleAtRequest).Epoch, MaxMintable: math.NewInt(1_000_000)}, nil
	case queryFeesInvariants:
		return &feestypes.QueryInvariantsResponse{
			CumulativeCollected: math.NewInt(l.feesPerBlock * height), CumulativeBurned: math.NewInt(height),
			CumulativeDistributed: math.ZeroInt(),
		}, nil
	case querySupplyOf:
		return &banktypes.QuerySupplyOfResponse{Amount: sdkCoin(l.supply(height))}, nil
	case queryBalance:
		return l.balance(req.(*banktypes.QueryBalanceRequest).Address)
	case queryValidators:
		return l.validatorPage(req.(*stakingtypes.QueryValidatorsRequest))
	case queryValidator:
		return l.validator(req.(*stakingtypes.QueryValidatorRequest).ValidatorAddr)
	case queryValidatorPower:
		return l.power(req.(*powertypes.QueryValidatorPowerRequest).OperatorAddress)
	}
	return nil, fmt.Errorf("the ledger answers no %s", method)
}

func (l *fakeLedger) balance(address string) (gogoproto.Message, error) {
	for _, name := range protocolModules {
		addr, err := moduleAddress(name)
		if err != nil {
			return nil, err
		}
		if addr == address {
			c := sdkCoin(l.balances[name])
			return &banktypes.QueryBalanceResponse{Balance: &c}, nil
		}
	}
	return nil, fmt.Errorf("%s is no module account", address)
}

func (l *fakeLedger) validatorPage(req *stakingtypes.QueryValidatorsRequest) (gogoproto.Message, error) {
	start := 0
	if len(req.Pagination.Key) == 8 {
		start = int(binary.BigEndian.Uint64(req.Pagination.Key))
	}
	end := min(start+int(req.Pagination.Limit), len(l.validators))
	resp := &stakingtypes.QueryValidatorsResponse{Pagination: &query.PageResponse{}}
	for _, v := range l.validators[start:end] {
		resp.Validators = append(resp.Validators, v.staking())
	}
	if end < len(l.validators) {
		resp.Pagination.NextKey = binary.BigEndian.AppendUint64(nil, uint64(end))
	}
	return resp, nil
}

func (l *fakeLedger) validator(operator string) (gogoproto.Message, error) {
	for _, v := range l.validators {
		if v.operator == operator {
			return &stakingtypes.QueryValidatorResponse{Validator: v.staking()}, nil
		}
	}
	return nil, fmt.Errorf("no validator %s", operator)
}

func (l *fakeLedger) power(operator string) (gogoproto.Message, error) {
	for _, v := range l.validators {
		if v.operator == operator {
			return &powertypes.QueryValidatorPowerResponse{OperatorAddress: operator, CometPower: v.power}, nil
		}
	}
	return nil, fmt.Errorf("no validator %s", operator)
}

// askedBeyondAggregates are the queries made for anything but the epoch, supply and validator
// aggregates, which the ledger answers.
func (f *fakeChain) askedBeyondAggregates() []string {
	aggregate := map[string]bool{
		queryEmissionState: true, queryEmissionParams: true, queryEmissionSchedule: true, queryFeesInvariants: true,
		querySupplyOf: true, queryBalance: true, queryValidators: true, queryValidator: true, queryValidatorPower: true,
	}
	var out []string
	for _, key := range f.asked {
		if method, _, _ := strings.Cut(key, "@"); !aggregate[method] {
			out = append(out, key)
		}
	}
	return out
}

func sdkCoin(n int64) sdk.Coin { return sdk.NewInt64Coin(params.BaseDenom, n) }

// votes is a commit of the validator set where signed[i] says whether member i voted for the block.
func votes(valset [][]byte, signed ...bool) []cmttypes.CommitSig {
	out := make([]cmttypes.CommitSig, len(valset))
	for i := range valset {
		if signed[i] {
			out[i] = cmttypes.CommitSig{BlockIDFlag: cmttypes.BlockIDFlagCommit, ValidatorAddress: valset[i]}
		} else {
			out[i] = cmttypes.NewCommitSigAbsent()
		}
	}
	return out
}
