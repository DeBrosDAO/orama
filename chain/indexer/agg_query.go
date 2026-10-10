package indexer

import (
	"context"
	"fmt"

	"cosmossdk.io/math"
	gogoproto "github.com/cosmos/gogoproto/proto"

	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/bech32"
	"github.com/cosmos/cosmos-sdk/types/query"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	distrtypes "github.com/cosmos/cosmos-sdk/x/distribution/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	archivetypes "github.com/DeBrosOfficial/network/chain/x/archive/types"
	cnfttypes "github.com/DeBrosOfficial/network/chain/x/cnft/types"
	emissiontypes "github.com/DeBrosOfficial/network/chain/x/emission/types"
	feestypes "github.com/DeBrosOfficial/network/chain/x/fees/types"
	houstypes "github.com/DeBrosOfficial/network/chain/x/houses/types"
	markettypes "github.com/DeBrosOfficial/network/chain/x/market/types"
	nodestypes "github.com/DeBrosOfficial/network/chain/x/nodes/types"
	powertypes "github.com/DeBrosOfficial/network/chain/x/power/types"
	relaytypes "github.com/DeBrosOfficial/network/chain/x/relay/types"
	shieldedtypes "github.com/DeBrosOfficial/network/chain/x/shielded/types"
	storagetypes "github.com/DeBrosOfficial/network/chain/x/storage/types"
	tokentypes "github.com/DeBrosOfficial/network/chain/x/token/types"
)

// The gRPC query methods the aggregates read at an epoch's closing height.
const (
	queryEmissionState    = "/orama.emission.v1.Query/CurrentEpoch"
	queryEmissionParams   = "/orama.emission.v1.Query/Params"
	queryEmissionSchedule = "/orama.emission.v1.Query/ScheduleAt"
	queryFeesInvariants   = "/orama.fees.v1.Query/Invariants"
	queryValidatorPower   = "/orama.power.v1.Query/ValidatorPower"
	queryValidators       = "/cosmos.staking.v1beta1.Query/Validators"
	queryBalance          = "/cosmos.bank.v1beta1.Query/Balance"
	querySupplyOf         = "/cosmos.bank.v1beta1.Query/SupplyOf"

	// validatorPageSize is the page size of the staking validator query.
	validatorPageSize = 100
)

// protocolModules is every module account of the app (app.GetMaccPerms): coins they hold belong to
// the protocol, not to a holder. A test keeps this list equal to the app's.
var protocolModules = []string{
	authtypes.FeeCollectorName,
	distrtypes.ModuleName,
	stakingtypes.BondedPoolName,
	stakingtypes.NotBondedPoolName,
	emissiontypes.ModuleName,
	powertypes.ModuleName,
	feestypes.ModuleName,
	feestypes.DepositsModuleName,
	tokentypes.ModuleName,
	archivetypes.ModuleName,
	nodestypes.ModuleName,
	houstypes.ModuleName,
	storagetypes.ModuleName,
	storagetypes.EscrowModuleName,
	storagetypes.ArchiveModuleName,
	relaytypes.ModuleName,
	cnfttypes.ModuleName,
	markettypes.ModuleName,
	shieldedtypes.ModuleName,
}

// moduleAddress is the bech32 account address of a module account.
func moduleAddress(name string) (string, error) {
	s, err := bech32.ConvertAndEncode(params.Bech32Prefix, authtypes.NewModuleAddress(name))
	if err != nil {
		return "", fmt.Errorf("failed to encode the address of module account %s: %w", name, err)
	}
	return s, nil
}

// chainReader asks the chain for its state at one height.
type chainReader struct {
	ctx    context.Context
	chain  Chain
	height int64
}

func (r chainReader) ask(method string, req, resp gogoproto.Message) error {
	if err := r.chain.QueryAt(r.ctx, r.height, method, req, resp); err != nil {
		return fmt.Errorf("query %s at height %d (the node must keep state for every height the index covers): %w", method, r.height, err)
	}
	return nil
}

func (r chainReader) emissionState() (emissiontypes.EpochState, error) {
	var resp emissiontypes.QueryCurrentEpochResponse
	err := r.ask(queryEmissionState, &emissiontypes.QueryCurrentEpochRequest{}, &resp)
	return resp.EpochState, err
}

func (r chainReader) emissionParams() (emissiontypes.Params, error) {
	var resp emissiontypes.QueryParamsResponse
	err := r.ask(queryEmissionParams, &emissiontypes.QueryParamsRequest{}, &resp)
	return resp.Params, err
}

func (r chainReader) maxMintable(epoch uint64) (math.Int, error) {
	var resp emissiontypes.QueryScheduleAtResponse
	err := r.ask(queryEmissionSchedule, &emissiontypes.QueryScheduleAtRequest{Epoch: epoch}, &resp)
	return resp.MaxMintable, err
}

// cumulatives reads the chain's running totals: x/emission's epoch state and x/fees' counters.
func (r chainReader) cumulatives(state emissiontypes.EpochState) (cumulatives, error) {
	var fees feestypes.QueryInvariantsResponse
	if err := r.ask(queryFeesInvariants, &feestypes.QueryInvariantsRequest{}, &fees); err != nil {
		return cumulatives{}, err
	}
	return cumulatives{
		Minted: state.CumulativeMinted, Burned: state.CumulativeBurned,
		Development: state.CumulativeDevelopmentMinted, Service: state.CumulativeServiceMinted,
		Faucet:        state.CumulativeFaucetMinted,
		FeesCollected: fees.CumulativeCollected, FeesBurned: fees.CumulativeBurned, FeesDistributed: fees.CumulativeDistributed,
	}, nil
}

func (r chainReader) balance(address string) (math.Int, error) {
	var resp banktypes.QueryBalanceResponse
	if err := r.ask(queryBalance, &banktypes.QueryBalanceRequest{Address: address, Denom: params.BaseDenom}, &resp); err != nil {
		return math.Int{}, err
	}
	if resp.Balance == nil {
		return math.ZeroInt(), nil
	}
	return resp.Balance.Amount, nil
}

func (r chainReader) totalSupply() (math.Int, error) {
	var resp banktypes.QuerySupplyOfResponse
	if err := r.ask(querySupplyOf, &banktypes.QuerySupplyOfRequest{Denom: params.BaseDenom}, &resp); err != nil {
		return math.Int{}, err
	}
	return resp.Amount.Amount, nil
}

// cometPower is the voting power CometBFT was given for a validator (x/power's LastPower).
func (r chainReader) cometPower(operator string) (int64, error) {
	var resp powertypes.QueryValidatorPowerResponse
	err := r.ask(queryValidatorPower, &powertypes.QueryValidatorPowerRequest{OperatorAddress: operator}, &resp)
	return resp.CometPower, err
}

// validators reads every staking validator, all statuses.
func (r chainReader) validators() ([]stakingtypes.Validator, error) {
	var out []stakingtypes.Validator
	var key []byte
	for {
		var resp stakingtypes.QueryValidatorsResponse
		req := &stakingtypes.QueryValidatorsRequest{Pagination: &query.PageRequest{Key: key, Limit: validatorPageSize}}
		if err := r.ask(queryValidators, req, &resp); err != nil {
			return nil, err
		}
		out = append(out, resp.Validators...)
		if resp.Pagination == nil || len(resp.Pagination.NextKey) == 0 {
			return out, nil
		}
		key = resp.Pagination.NextKey
	}
}

// validatorStatus names an x/staking bond status.
func validatorStatus(s stakingtypes.BondStatus) string {
	switch s {
	case stakingtypes.Bonded:
		return StatusBonded
	case stakingtypes.Unbonding:
		return StatusUnbonding
	default:
		return StatusUnbonded
	}
}

// consensusAddress derives a validator's 20-byte consensus address from the public key it signs with.
func (f *Follower) consensusAddress(v stakingtypes.Validator) ([]byte, error) {
	var pk cryptotypes.PubKey
	if err := f.codec.UnpackAny(v.ConsensusPubkey, &pk); err != nil {
		return nil, fmt.Errorf("failed to read the consensus key of validator %s: %w", v.OperatorAddress, err)
	}
	return pk.Address().Bytes(), nil
}

// consBech32 is the bech32 form of a consensus address, as x/slashing's events carry it.
func consBech32(cons []byte) (string, error) {
	s, err := bech32.ConvertAndEncode(params.Bech32PrefixConsAddr, cons)
	if err != nil {
		return "", fmt.Errorf("failed to encode consensus address %x: %w", cons, err)
	}
	return s, nil
}

// sumCoins returns the norama in a coin string such as "12norama" or "12norama,3other"; an empty
// string is zero.
func sumCoins(raw string) (math.Int, error) {
	if raw == "" {
		return math.ZeroInt(), nil
	}
	coins, err := sdk.ParseCoinsNormalized(raw)
	if err != nil {
		return math.Int{}, fmt.Errorf("amount %q is not a coin list: %w", raw, err)
	}
	return coins.AmountOf(params.BaseDenom), nil
}
