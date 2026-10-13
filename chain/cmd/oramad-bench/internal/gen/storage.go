package gen

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"cosmossdk.io/math"

	"github.com/DeBrosOfficial/network/chain/piece"
	storagetypes "github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// StorageDeal is the type name of the creation of a storage deal.
const StorageDeal = "storage-deal"

const (
	// dealPricePerEpoch is what each replica is paid per epoch, in norama. The deal escrows
	// price x replicas x epochs, so a small price keeps thousands of deals cheap.
	dealPricePerEpoch = 1_000
	dealEpochs        = 1
	// dealPayloadSeed makes the payload of the piece every deal commits to.
	dealPayloadSeed = "oramad-bench storage payload"
)

func init() { register(StorageDeal, func() Generator { return &storageDeal{} }) }

// storageDeal creates PUBLIC_PIN deals over one piece commitment. A deal is a record the chain
// creates and later assigns to providers; creating it needs no provider, so the type measures the
// chain's cost of a deal, not of storing data.
type storageDeal struct {
	runID      string
	accounts   []string
	commitment storagetypes.PieceCommitment
}

func (s *storageDeal) Name() string { return StorageDeal }

func (s *storageDeal) Describe() string {
	return "x/storage MsgCreateDeal: a PUBLIC_PIN deal of 3 replicas over one commitment (creation only; no provider stores data)"
}

func (s *storageDeal) Prepare(ctx context.Context, env *Env, _ int) error {
	if len(env.Accounts) == 0 {
		return errors.New("storage-deal needs an account")
	}
	var params storagetypes.QueryParamsResponse
	if err := env.Chain.Client.Query(ctx, "/orama.storage.v1.Query/Params", &storagetypes.QueryParamsRequest{}, &params); err != nil {
		return Skipf("the chain does not serve the storage params: %v", err)
	}
	size := int(params.Params.MinDealBytes)
	payload := make([]byte, size)
	h := sha256.Sum256([]byte(dealPayloadSeed))
	for i := range payload {
		payload[i] = h[i%len(h)]
	}
	c, err := piece.Commit(payload)
	if err != nil {
		return fmt.Errorf("commit the deal payload: %w", err)
	}
	s.commitment = storagetypes.PieceCommitment{Root: c.Root, RealLeafCount: c.RealLeafCount, PaddedLeafCount: c.PaddedLeafCount, PieceBytes: uint64(size)}
	s.runID = env.RunID
	s.accounts = make([]string, len(env.Accounts))
	for i, a := range env.Accounts {
		s.accounts[i] = a.Address
	}
	return nil
}

func (s *storageDeal) Available() int { return Unlimited }

func (s *storageDeal) Plan(k int) (Planned, error) {
	acct := roundRobin(k, len(s.accounts))
	nonce := sha256.Sum256([]byte(fmt.Sprintf("%s|deal|%d", s.runID, k)))
	msg := &storagetypes.MsgCreateDeal{
		Signer: s.accounts[acct], Class: storagetypes.DealClass_DEAL_CLASS_PUBLIC_PIN,
		DealNonce: nonce[:], Replicas: storagetypes.MinReplicas,
		PricePerEpoch: math.NewInt(dealPricePerEpoch), DurationEpochs: dealEpochs,
		Pieces: []storagetypes.PieceCommitment{s.commitment},
	}
	return Planned{Account: acct, Msg: msg}, nil
}
