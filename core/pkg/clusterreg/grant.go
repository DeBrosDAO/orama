package clusterreg

import "fmt"

const (
	// GrantDealTypeURL is the Any type URL of orama.storage.v1.MsgGrantDealAuthorization.
	GrantDealTypeURL = "/orama.storage.v1.MsgGrantDealAuthorization"
	// RevokeDealTypeURL is the Any type URL of orama.storage.v1.MsgRevokeDealAuthorization.
	RevokeDealTypeURL = "/orama.storage.v1.MsgRevokeDealAuthorization"

	minGrantReplicas     = 3
	maxGrantReplicas     = 32
	maxGrantDurationEpox = 1_000_000
)

// Grant is MsgGrantDealAuthorization. SpendLimit is a positive decimal norama
// amount with no leading zero, the text cosmos-sdk math.Int marshals.
type Grant struct {
	Signer        string
	Grantee       string
	SpendLimit    string
	PeriodEpochs  uint64
	MaxPieceBytes uint64
	MaxDuration   uint64
	Replicas      uint32
	ExpiryEpoch   uint64
}

// ValidateGrant checks the stateless rules of MsgGrantDealAuthorization.
func ValidateGrant(g Grant) error {
	signer, err := CanonicalAccount(g.Signer)
	if err != nil {
		return fmt.Errorf("signer: %w", err)
	}
	grantee, err := CanonicalAccount(g.Grantee)
	if err != nil {
		return fmt.Errorf("grantee: %w", err)
	}
	if signer == grantee {
		return fmt.Errorf("granter and grantee must differ")
	}
	if !positiveInteger(g.SpendLimit) {
		return fmt.Errorf("spend limit must be a positive integer of norama")
	}
	if g.MaxPieceBytes == 0 {
		return fmt.Errorf("max piece bytes must be positive")
	}
	if g.MaxDuration == 0 || g.MaxDuration > maxGrantDurationEpox {
		return fmt.Errorf("max duration epochs must be in [1, %d]", maxGrantDurationEpox)
	}
	if g.Replicas < minGrantReplicas || g.Replicas > maxGrantReplicas {
		return fmt.Errorf("replicas must be in [%d, %d]", minGrantReplicas, maxGrantReplicas)
	}
	return nil
}

// EncodeGrant is the protobuf orama.storage.v1.MsgGrantDealAuthorization.
// Zero period and expiry are omitted, matching proto3.
func EncodeGrant(g Grant) []byte {
	out := appendStringField(nil, 1, g.Signer)
	out = appendStringField(out, 2, g.Grantee)
	out = appendStringField(out, 3, g.SpendLimit)
	if g.PeriodEpochs != 0 {
		out = appendUvarintField(out, 4, g.PeriodEpochs)
	}
	if g.MaxPieceBytes != 0 {
		out = appendUvarintField(out, 5, g.MaxPieceBytes)
	}
	if g.MaxDuration != 0 {
		out = appendUvarintField(out, 6, g.MaxDuration)
	}
	if g.Replicas != 0 {
		out = appendUvarintField(out, 7, uint64(g.Replicas))
	}
	if g.ExpiryEpoch != 0 {
		out = appendUvarintField(out, 8, g.ExpiryEpoch)
	}
	return out
}

// EncodeRevokeGrant is the protobuf orama.storage.v1.MsgRevokeDealAuthorization.
func EncodeRevokeGrant(signer, grantee string) []byte {
	out := appendStringField(nil, 1, signer)
	return appendStringField(out, 2, grantee)
}
