// Package operatorview is the operator's own account on the chain, as `orama status` shows it: the
// earnings x/fees holds for the account, its spendable balance and what it has bonded. The cluster
// half of the status comes from the operator telemetry; this half comes from the chain, through the
// gateway's /v1/chain/query/ route.
package operatorview

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The chain queries the summary reads.
const (
	queryEarnings    = "orama.fees.v1.Query/Earnings"
	queryBalance     = "cosmos.bank.v1beta1.Query/Balance"
	queryDelegations = "cosmos.staking.v1beta1.Query/DelegatorDelegations"
	queryRelays      = "orama.relay.v1.Query/OperatorRelays"

	// denom is the chain's base denomination; amounts are in it.
	denom = "norama"
	// pageLimit is the most delegations one summary reads; an operator bonds a handful.
	pageLimit = 100
	// relayPageLimit is how many relays one read of x/relay asks for (the chain clamps a page to 100) and
	// relayPageMax how many pages a summary follows: an operator runs a handful of relays, and one
	// that runs more has the rest named by RelaysMore.
	relayPageLimit = 100
	relayPageMax   = 5
	// fingerprintBytes is the length of a Tor relay's RSA fingerprint.
	fingerprintBytes = 20
)

// nodeIDPattern is what x/nodes accepts as a node id. A relay's node id is printed on a terminal, so one
// that is not this (a gateway that answers with control characters) is refused, not shown.
var nodeIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$`)

// Querier runs one module query and answers its JSON.
type Querier interface {
	Query(ctx context.Context, query, request string) (json.RawMessage, error)
}

// Summary is one reading of the operator's account. Amounts are decimal norama strings, exact at
// any size. Err names the first query that failed; the amounts read before it are kept.
type Summary struct {
	Address   string `json:"address"`
	Earnings  string `json:"earnings_norama"`
	Spendable string `json:"spendable_norama"`
	Bonded    string `json:"bonded_norama"`
	// Relays are the Tor relays registered on the operator's nodes, each with the last payout it
	// received. Their pay is part of Earnings: x/relay credits it to the operator's earnings.
	Relays []Relay `json:"relays"`
	// RelaysMore is true when the operator has more relays than Relays lists.
	RelaysMore bool      `json:"relays_more,omitempty"`
	ReadAt     time.Time `json:"read_at"`
	Err        string    `json:"error,omitempty"`
}

// Fetch reads the account at address through q.
func Fetch(ctx context.Context, q Querier, address string, now time.Time) Summary {
	s := Summary{Address: address, Relays: []Relay{}, ReadAt: now.UTC()}
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
	var err error
	if s.Relays, s.RelaysMore, err = relays(ctx, q, address); err != nil {
		s.Err = fmt.Sprintf("read the relays of %s: %v", address, err)
	}
	return s
}

// Relay is one relay registered on the operator's nodes, with the last payout x/relay made it.
type Relay struct {
	NodeID string `json:"node_id"`
	// Fingerprint is the relay's RSA identity digest, 40 upper-case hex digits.
	Fingerprint string `json:"rsa_fingerprint"`
	Exit        bool   `json:"exit"`
	Jailed      bool   `json:"jailed"`
	// LastPayoutEpoch is the epoch of the newest payout; 0 for a relay never paid.
	LastPayoutEpoch uint64 `json:"last_payout_epoch"`
	// LastPayout is that payout in norama, before the 90/5/5 split of the operator's total; "0" when never paid.
	LastPayout string `json:"last_payout_norama"`
}

// relays reads the operator's relays from x/relay, following the pages up to relayPageMax; more is
// true when the operator has relays beyond them.
func relays(ctx context.Context, q Querier, address string) (out []Relay, more bool, err error) {
	out = []Relay{}
	key := ""
	for page := 0; page < relayPageMax; page++ {
		pagination := map[string]any{"limit": fmt.Sprint(relayPageLimit)}
		if key != "" {
			pagination["key"] = key
		}
		var answer struct {
			Relays []struct {
				Relay struct {
					NodeID         string `json:"node_id"`
					RSAFingerprint string `json:"rsa_fingerprint"`
					Exit           bool   `json:"exit"`
					Jailed         bool   `json:"jailed"`
				} `json:"relay"`
				LastPayoutEpoch  string `json:"last_payout_epoch"`
				LastPayoutAmount string `json:"last_payout_amount"`
			} `json:"relays"`
			Pagination struct {
				NextKey string `json:"next_key"`
			} `json:"pagination"`
		}
		if err := query(ctx, q, queryRelays, map[string]any{"operator": address, "pagination": pagination}, &answer); err != nil {
			return nil, false, err
		}
		for _, r := range answer.Relays {
			relay, err := relayRow(r.Relay.NodeID, r.Relay.RSAFingerprint, r.LastPayoutEpoch, r.LastPayoutAmount)
			if err != nil {
				return nil, false, err
			}
			relay.Exit, relay.Jailed = r.Relay.Exit, r.Relay.Jailed
			out = append(out, relay)
		}
		if key = answer.Pagination.NextKey; key == "" {
			return out, false, nil
		}
	}
	return out, true, nil
}

// relayRow checks what the chain said of one relay: the fingerprint is 20 bytes of base64, the epoch
// a number and the amount a non-negative integer. An empty epoch or amount is "never paid".
func relayRow(nodeID, fingerprint, epoch, payout string) (Relay, error) {
	if !nodeIDPattern.MatchString(nodeID) {
		return Relay{}, fmt.Errorf("the chain names a relay's node %q, which is not a node id", nodeID)
	}
	raw, err := base64.StdEncoding.DecodeString(fingerprint)
	if err != nil || len(raw) != fingerprintBytes {
		return Relay{}, fmt.Errorf("the relay of node %q has a fingerprint %q that is not %d bytes of base64", nodeID, fingerprint, fingerprintBytes)
	}
	relay := Relay{NodeID: nodeID, Fingerprint: strings.ToUpper(hex.EncodeToString(raw))}
	if epoch != "" {
		if relay.LastPayoutEpoch, err = strconv.ParseUint(epoch, 10, 64); err != nil {
			return Relay{}, fmt.Errorf("the relay of node %q has a last payout epoch %q that is not a number", nodeID, epoch)
		}
	}
	if relay.LastPayout, err = amount(payout); err != nil {
		return Relay{}, fmt.Errorf("the relay of node %q: %w", nodeID, err)
	}
	return relay, nil
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
