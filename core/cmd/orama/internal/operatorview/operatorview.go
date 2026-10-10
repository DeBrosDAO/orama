// Package operatorview is the operator's own account on the chain, as `orama status` shows it: the
// earnings x/fees holds for the account, its spendable balance and what it has bonded. The cluster
// half of the status comes from the operator telemetry; this half comes from the chain, through the
// gateway's /v1/chain/query/ route.
package operatorview

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"time"
)

// The chain queries the summary reads.
const (
	queryEarnings    = "orama.fees.v1.Query/Earnings"
	queryBalance     = "cosmos.bank.v1beta1.Query/Balance"
	queryDelegations = "cosmos.staking.v1beta1.Query/DelegatorDelegations"

	// denom is the chain's base denomination; amounts are in it.
	denom = "norama"
	// pageLimit is the most delegations one summary reads; an operator bonds a handful.
	pageLimit = 100
)

// Querier runs one module query and answers its JSON.
type Querier interface {
	Query(ctx context.Context, query, request string) (json.RawMessage, error)
}

// Summary is one reading of the operator's account. Amounts are decimal norama strings, exact at
// any size. Err names the first query that failed; the amounts read before it are kept.
type Summary struct {
	Address   string    `json:"address"`
	Earnings  string    `json:"earnings_norama"`
	Spendable string    `json:"spendable_norama"`
	Bonded    string    `json:"bonded_norama"`
	ReadAt    time.Time `json:"read_at"`
	Err       string    `json:"error,omitempty"`
}

// Fetch reads the account at address through q.
func Fetch(ctx context.Context, q Querier, address string, now time.Time) Summary {
	s := Summary{Address: address, ReadAt: now.UTC()}
	steps := []struct {
		what string
		read func() (string, error)
		into *string
	}{
		{"earnings", func() (string, error) { return earnings(ctx, q, address) }, &s.Earnings},
		{"balance", func() (string, error) { return balance(ctx, q, address) }, &s.Spendable},
		{"bonded", func() (string, error) { return bonded(ctx, q, address) }, &s.Bonded},
	}
	for _, step := range steps {
		v, err := step.read()
		if err != nil {
			s.Err = fmt.Sprintf("read the %s of %s: %v", step.what, address, err)
			return s
		}
		*step.into = v
	}
	return s
}

func earnings(ctx context.Context, q Querier, address string) (string, error) {
	var out struct {
		Balance string `json:"balance"`
	}
	if err := query(ctx, q, queryEarnings, map[string]string{"address": address}, &out); err != nil {
		return "", err
	}
	return amount(out.Balance)
}

func balance(ctx context.Context, q Querier, address string) (string, error) {
	var out struct {
		Balance struct {
			Amount string `json:"amount"`
		} `json:"balance"`
	}
	if err := query(ctx, q, queryBalance, map[string]string{"address": address, "denom": denom}, &out); err != nil {
		return "", err
	}
	return amount(out.Balance.Amount)
}

func bonded(ctx context.Context, q Querier, address string) (string, error) {
	var out struct {
		Responses []struct {
			Balance struct {
				Amount string `json:"amount"`
			} `json:"balance"`
		} `json:"delegation_responses"`
	}
	req := map[string]any{"delegator_addr": address, "pagination": map[string]any{"limit": fmt.Sprint(pageLimit)}}
	if err := query(ctx, q, queryDelegations, req, &out); err != nil {
		return "", err
	}
	total := new(big.Int)
	for _, r := range out.Responses {
		v, err := nonNegative(r.Balance.Amount)
		if err != nil {
			return "", fmt.Errorf("a delegation: %w", err)
		}
		total.Add(total, v)
	}
	return total.String(), nil
}

func query(ctx context.Context, q Querier, name string, request any, out any) error {
	raw, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("encode the %s request: %w", name, err)
	}
	answer, err := q.Query(ctx, name, string(raw))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(answer, out); err != nil {
		return fmt.Errorf("decode the %s answer: %w", name, err)
	}
	return nil
}

// amount checks that s is a non-negative integer and returns it; an empty answer is zero.
func amount(s string) (string, error) {
	v, err := nonNegative(s)
	if err != nil {
		return "", err
	}
	return v.String(), nil
}

func nonNegative(s string) (*big.Int, error) {
	if s == "" {
		return new(big.Int), nil
	}
	v, ok := new(big.Int).SetString(s, 10)
	if !ok || v.Sign() < 0 {
		return nil, fmt.Errorf("the amount %q is not a non-negative integer", s)
	}
	return v, nil
}
