package keeper

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/query"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/DeBrosOfficial/network/chain/x/internal/pagebound"
	"github.com/DeBrosOfficial/network/chain/x/relay/types"
)

var _ types.QueryServer = queryServer{}

type queryServer struct {
	Keeper
}

// NewQueryServerImpl returns an implementation of x/relay's QueryServer.
func NewQueryServerImpl(k Keeper) types.QueryServer {
	return queryServer{Keeper: k}
}

func (q queryServer) Params(goCtx context.Context, _ *types.QueryParamsRequest) (*types.QueryParamsResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	params, err := q.Keeper.Params.Get(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryParamsResponse{Params: params}, nil
}

// Reporters lists the reporter set. Every writer of the set (genesis, governance) refuses more than
// types.MaxReporters entries, and the query stops there as well, so a public query never walks a set
// that something wrote past the bound.
func (q queryServer) Reporters(goCtx context.Context, _ *types.QueryReportersRequest) (*types.QueryReportersResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	var reporters []string
	if err := q.Keeper.Reporters.Walk(ctx, nil, func(addr string, _ bool) (bool, error) {
		reporters = append(reporters, addr)
		return len(reporters) >= types.MaxReporters, nil
	}); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if reporters == nil {
		reporters = []string{}
	}
	return &types.QueryReportersResponse{Reporters: reporters}, nil
}

func (q queryServer) Relay(goCtx context.Context, req *types.QueryRelayRequest) (*types.QueryRelayResponse, error) {
	if req == nil || req.RsaFingerprintHex == "" {
		return nil, status.Error(codes.InvalidArgument, "rsa_fingerprint_hex is required")
	}
	fingerprint, err := hex.DecodeString(req.RsaFingerprintHex)
	if err != nil || len(fingerprint) != types.RSAFingerprintLen {
		return nil, status.Error(codes.InvalidArgument, "rsa_fingerprint_hex must be 20 bytes of hex")
	}
	ctx := sdk.UnwrapSDKContext(goCtx)
	relay, found, err := q.Keeper.getRelay(ctx, fingerprint)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if !found {
		return nil, status.Error(codes.NotFound, "relay not found")
	}
	return &types.QueryRelayResponse{Relay: relay}, nil
}

// MaxRelaysPerPage bounds one Relays page.
const MaxRelaysPerPage = 100

// Relays pages through every registered relay in RSA fingerprint order. A page key resumes after
// the last relay of the previous page.
func (q queryServer) Relays(goCtx context.Context, req *types.QueryRelaysRequest) (*types.QueryRelaysResponse, error) {
	if req == nil {
		req = &types.QueryRelaysRequest{}
	}
	pagination, err := pagebound.Clamp(req.Pagination, MaxRelaysPerPage)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	relays, page, err := query.CollectionPaginate(sdk.UnwrapSDKContext(goCtx), q.Keeper.Relays, pagination,
		func(_ []byte, relay types.Relay) (types.Relay, error) { return relay, nil })
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if relays == nil {
		relays = []types.Relay{}
	}
	return &types.QueryRelaysResponse{Relays: relays, Pagination: page}, nil
}

func (q queryServer) EpochResult(goCtx context.Context, req *types.QueryEpochResultRequest) (*types.QueryEpochResultResponse, error) {
	if req == nil || req.Epoch == 0 {
		return nil, status.Error(codes.InvalidArgument, "epoch is required")
	}
	ctx := sdk.UnwrapSDKContext(goCtx)
	result, err := q.Keeper.EpochResults.Get(ctx, req.Epoch)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	return &types.QueryEpochResultResponse{Result: result}, nil
}

func (q queryServer) Invariants(goCtx context.Context, _ *types.QueryInvariantsRequest) (*types.QueryInvariantsResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	got, err := q.Keeper.CheckInvariants(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryInvariantsResponse{
		CeilingHolds: got.CeilingHolds,
		PayoutsMatch: got.PayoutsMatch,
		Detail:       got.Detail,
	}, nil
}

// OperatorRelays pages through the relays an operator has registered, in RSA fingerprint order, each
// with the last payout it received. A relay that was never paid has last_payout_epoch 0.
func (q queryServer) OperatorRelays(goCtx context.Context, req *types.QueryOperatorRelaysRequest) (*types.QueryOperatorRelaysResponse, error) {
	if req == nil || req.Operator == "" {
		return nil, status.Error(codes.InvalidArgument, "operator is required")
	}
	operator, err := types.CanonicalAddress(req.Operator)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	pagination, err := pagebound.Clamp(req.Pagination, MaxRelaysPerPage)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	ctx := sdk.UnwrapSDKContext(goCtx)
	standings, page, err := query.CollectionPaginate(ctx, q.Keeper.OperatorIndex, pagination,
		func(key collections.Pair[string, []byte], _ collections.NoValue) (types.RelayStanding, error) {
			return q.Keeper.standingOf(ctx, key.K2())
		}, query.WithCollectionPaginationPairPrefix[string, []byte](operator))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if standings == nil {
		standings = []types.RelayStanding{}
	}
	return &types.QueryOperatorRelaysResponse{Relays: standings, Pagination: page}, nil
}

// standingOf is a registered relay with its last payout.
func (k Keeper) standingOf(ctx sdk.Context, fingerprint []byte) (types.RelayStanding, error) {
	relay, found, err := k.getRelay(ctx, fingerprint)
	if err != nil {
		return types.RelayStanding{}, err
	}
	if !found {
		return types.RelayStanding{}, fmt.Errorf("operator index names relay %X that is not stored", fingerprint)
	}
	standing := types.RelayStanding{Relay: relay, LastPayoutAmount: math.ZeroInt()}
	last, err := k.LastPayouts.Get(ctx, fingerprint)
	switch {
	case err == nil:
		standing.LastPayoutEpoch, standing.LastPayoutAmount = last.Epoch, last.Amount
	case !errors.Is(err, collections.ErrNotFound):
		return types.RelayStanding{}, fmt.Errorf("failed to load the last payout of relay %X: %w", fingerprint, err)
	}
	return standing, nil
}

// RelayPayouts pages through the payouts of one settled epoch in RSA fingerprint order. An epoch that
// paid nobody, or was never settled, has none.
func (q queryServer) RelayPayouts(goCtx context.Context, req *types.QueryRelayPayoutsRequest) (*types.QueryRelayPayoutsResponse, error) {
	if req == nil || req.Epoch == 0 {
		return nil, status.Error(codes.InvalidArgument, "epoch is required")
	}
	pagination, err := pagebound.Clamp(req.Pagination, MaxRelaysPerPage)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	payouts, page, err := query.CollectionPaginate(sdk.UnwrapSDKContext(goCtx), q.Keeper.Payouts, pagination,
		func(_ collections.Pair[uint64, []byte], payout types.RelayPayout) (types.RelayPayout, error) {
			return payout, nil
		},
		query.WithCollectionPaginationPairPrefix[uint64, []byte](req.Epoch))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if payouts == nil {
		payouts = []types.RelayPayout{}
	}
	return &types.QueryRelayPayoutsResponse{Payouts: payouts, Pagination: page}, nil
}
