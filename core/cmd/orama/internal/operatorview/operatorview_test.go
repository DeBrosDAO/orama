package operatorview

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// fakeChain answers each query from answers; a query it does not know fails.
type fakeChain struct {
	answers map[string]string
	fail    map[string]error
	asked   []string
}

func (f *fakeChain) Query(_ context.Context, query, request string) (json.RawMessage, error) {
	f.asked = append(f.asked, query+" "+request)
	if err := f.fail[query]; err != nil {
		return nil, err
	}
	a, ok := f.answers[query]
	if !ok {
		return nil, errors.New("unknown query " + query)
	}
	return json.RawMessage(a), nil
}

const addr = "orama1operatoraddress"

func TestFetch_readsEarningsBalanceAndBondedSum(t *testing.T) {
	f := &fakeChain{answers: map[string]string{
		queryEarnings:    `{"balance":"123456789012345678901234567890"}`,
		queryBalance:     `{"balance":{"denom":"norama","amount":"42"}}`,
		queryDelegations: `{"delegation_responses":[{"balance":{"amount":"1000000000000"}},{"balance":{"amount":"5"}}]}`,
	}}
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.FixedZone("x", 3600))
	s := Fetch(context.Background(), f, addr, now)
	if s.Err != "" || s.Earnings != "123456789012345678901234567890" || s.Spendable != "42" || s.Bonded != "1000000000005" {
		t.Fatalf("summary %+v", s)
	}
	if !s.ReadAt.Equal(now) || s.ReadAt.Location() != time.UTC {
		t.Errorf("read_at %v, want %v in UTC", s.ReadAt, now)
	}
	if !strings.Contains(f.asked[1], `"denom":"norama"`) || !strings.Contains(f.asked[2], `"delegator_addr":"`+addr+`"`) {
		t.Errorf("requests %v", f.asked)
	}
}

// An account the chain has never seen answers empty amounts: zero, not an error.
func TestFetch_emptyAnswersAreZero(t *testing.T) {
	f := &fakeChain{answers: map[string]string{
		queryEarnings: `{}`, queryBalance: `{"balance":{}}`, queryDelegations: `{"delegation_responses":[]}`,
	}}
	s := Fetch(context.Background(), f, addr, time.Now())
	if s.Err != "" || s.Earnings != "0" || s.Spendable != "0" || s.Bonded != "0" {
		t.Fatalf("summary %+v", s)
	}
}

// A failed read stops the summary with the step named; what was read before it is kept, and a
// malformed or negative amount is a failure, not a silent zero.
func TestFetch_failuresNameTheStepAndKeepEarlierReads(t *testing.T) {
	f := &fakeChain{
		answers: map[string]string{queryEarnings: `{"balance":"7"}`},
		fail:    map[string]error{queryBalance: errors.New("gateway answered 502")},
	}
	s := Fetch(context.Background(), f, addr, time.Now())
	if s.Earnings != "7" || s.Spendable != "" || !strings.Contains(s.Err, "balance") || !strings.Contains(s.Err, "502") {
		t.Fatalf("summary %+v", s)
	}
	for name, answer := range map[string]string{"negative": `{"balance":"-1"}`, "not a number": `{"balance":"1e9"}`, "not JSON": `{`} {
		f := &fakeChain{answers: map[string]string{queryEarnings: answer}}
		if s := Fetch(context.Background(), f, addr, time.Now()); s.Err == "" || s.Earnings != "" {
			t.Errorf("%s: summary %+v", name, s)
		}
	}
}
