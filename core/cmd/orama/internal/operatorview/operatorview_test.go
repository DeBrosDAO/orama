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
		queryRelays:      `{"relays":[]}`,
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
		queryEarnings: `{}`, queryBalance: `{"balance":{}}`, queryDelegations: `{"delegation_responses":[]}`, queryRelays: `{}`,
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

// fingerprint20 is 20 bytes of 0xab as protojson writes them (base64), and its hex.
const (
	fingerprint20    = "q6urq6urq6urq6urq6urq6urq6s="
	fingerprint20Hex = "ABABABABABABABABABABABABABABABABABABABAB"
)

func accountAnswers() map[string]string {
	return map[string]string{
		queryEarnings: `{"balance":"7"}`, queryBalance: `{"balance":{"amount":"1"}}`, queryDelegations: `{"delegation_responses":[]}`,
	}
}

func TestFetch_listsTheOperatorsRelaysWithTheirLastPayout(t *testing.T) {
	answers := accountAnswers()
	answers[queryRelays] = `{"relays":[
		{"relay":{"node_id":"node-a","rsa_fingerprint":"` + fingerprint20 + `","exit":true},"last_payout_epoch":"12","last_payout_amount":"4000"},
		{"relay":{"node_id":"node-b","rsa_fingerprint":"` + fingerprint20 + `","jailed":true},"last_payout_epoch":"0","last_payout_amount":"0"}]}`
	f := &fakeChain{answers: answers}
	s := Fetch(context.Background(), f, addr, time.Now())
	if s.Err != "" || len(s.Relays) != 2 || s.RelaysMore {
		t.Fatalf("summary %+v", s)
	}
	want := Relay{NodeID: "node-a", Fingerprint: fingerprint20Hex, Exit: true, LastPayoutEpoch: 12, LastPayout: "4000"}
	if s.Relays[0] != want {
		t.Errorf("relay %+v, want %+v", s.Relays[0], want)
	}
	if never := s.Relays[1]; never.LastPayoutEpoch != 0 || never.LastPayout != "0" || !never.Jailed || never.Exit {
		t.Errorf("a relay never paid: %+v", never)
	}
	if !strings.Contains(f.asked[3], `"operator":"`+addr+`"`) {
		t.Errorf("relay request %q", f.asked[3])
	}
}

func TestFetch_followsTheRelayPagesAndSaysWhenThereAreMore(t *testing.T) {
	answers := accountAnswers()
	answers[queryRelays] = `{"relays":[{"relay":{"node_id":"n","rsa_fingerprint":"` + fingerprint20 + `"}}],"pagination":{"next_key":"AQ=="}}`
	f := &fakeChain{answers: answers}
	s := Fetch(context.Background(), f, addr, time.Now())
	if s.Err != "" || len(s.Relays) != relayPageMax || !s.RelaysMore {
		t.Fatalf("%d relays, more %v, err %q", len(s.Relays), s.RelaysMore, s.Err)
	}
	if !strings.Contains(f.asked[4], `"key":"AQ=="`) {
		t.Errorf("the second page did not ask by the key: %q", f.asked[4])
	}
}

func TestFetch_aRelayTheChainDescribesBadlyIsAnErrorAndKeepsTheAmounts(t *testing.T) {
	for name, relay := range map[string]string{
		"a short fingerprint":           `{"relay":{"node_id":"n","rsa_fingerprint":"AAAA"}}`,
		"an epoch that is not a number": `{"relay":{"node_id":"n","rsa_fingerprint":"` + fingerprint20 + `"},"last_payout_epoch":"x"}`,
		"a negative payout":             `{"relay":{"node_id":"n","rsa_fingerprint":"` + fingerprint20 + `"},"last_payout_amount":"-5"}`,
	} {
		t.Run(name, func(t *testing.T) {
			answers := accountAnswers()
			answers[queryRelays] = `{"relays":[` + relay + `]}`
			s := Fetch(context.Background(), &fakeChain{answers: answers}, addr, time.Now())
			if !strings.Contains(s.Err, "relays") || s.Earnings != "7" || len(s.Relays) != 0 {
				t.Fatalf("summary %+v", s)
			}
		})
	}
}

func TestFetch_aFailedRelayReadNamesItselfAndKeepsTheAmounts(t *testing.T) {
	f := &fakeChain{answers: accountAnswers(), fail: map[string]error{queryRelays: errors.New("gateway answered 502")}}
	s := Fetch(context.Background(), f, addr, time.Now())
	if s.Earnings != "7" || !strings.Contains(s.Err, "relays") || !strings.Contains(s.Err, "502") {
		t.Fatalf("summary %+v", s)
	}
}
