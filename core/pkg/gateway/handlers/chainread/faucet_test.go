package chainread

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/chainfaucet"
)

const faucetRecipient = "orama1fvfzzvqv2ara2crn3z352zjhnfl0tw4rk82j53"

// fakeFaucet records the drips it is asked for and answers from its fields.
type fakeFaucet struct {
	mu      sync.Mutex
	drips   []drip
	err     error
	block   chan struct{}
	started chan struct{}
}

type drip struct {
	recipient string
	amount    string
}

func (f *fakeFaucet) Drip(ctx context.Context, recipient string, amount *big.Int) (*chainfaucet.Dripped, error) {
	f.mu.Lock()
	f.drips = append(f.drips, drip{recipient, amount.String()})
	f.mu.Unlock()
	if f.started != nil {
		f.started <- struct{}{}
	}
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	return &chainfaucet.Dripped{TxHash: strings.Repeat("AB", 32), Height: 1234, Amount: amount}, nil
}

func (f *fakeFaucet) got() []drip {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]drip(nil), f.drips...)
}

func faucetProxy(t *testing.T, f FaucetService) *Proxy {
	t.Helper()
	p, err := New(Config{RPCURL: "http://127.0.0.1:1", RESTURL: "http://127.0.0.1:1", IndexURL: "http://127.0.0.1:1", Faucet: f})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func postFaucet(p *Proxy, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, mountPrefix+faucetPath, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	return rec
}

func faucetErrorOf(t *testing.T, rec *httptest.ResponseRecorder) faucetError {
	t.Helper()
	var e faucetError
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil || e.Error == "" {
		t.Fatalf("body %q is not a faucet error: %v", rec.Body.String(), err)
	}
	return e
}

func TestFaucet_aDripIsAnsweredWithItsHashAmountAndBlock(t *testing.T) {
	f := &fakeFaucet{}
	rec := postFaucet(faucetProxy(t, f), `{"recipient":"`+faucetRecipient+`","amount":"5000000000"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"tx_hash": strings.Repeat("AB", 32), "amount": "5000000000", "height": "1234"}
	if len(got) != len(want) || got["tx_hash"] != want["tx_hash"] || got["amount"] != want["amount"] || got["height"] != want["height"] {
		t.Errorf("answer = %v, want %v (64-bit integers are decimal strings)", got, want)
	}
	if d := f.got(); len(d) != 1 || d[0] != (drip{faucetRecipient, "5000000000"}) {
		t.Errorf("drips = %v", d)
	}
	for header, want := range map[string]string{"Cache-Control": "no-store", "Content-Type": "application/json"} {
		if rec.Header().Get(header) != want {
			t.Errorf("%s = %q, want %q", header, rec.Header().Get(header), want)
		}
	}
}

func TestFaucet_noAmountIsTheDefaultDrip(t *testing.T) {
	f := &fakeFaucet{}
	rec := postFaucet(faucetProxy(t, f), `{"recipient":"`+faucetRecipient+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if d := f.got(); len(d) != 1 || d[0].amount != "100000000000" {
		t.Errorf("drips = %v, want the default of 100 ORAMA", d)
	}
}

func TestFaucet_withoutAFaucetKeyTheRouteIsNotThere(t *testing.T) {
	rec := postFaucet(faucetProxy(t, nil), `{"recipient":"`+faucetRecipient+`"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	get := httptest.NewRecorder()
	faucetProxy(t, nil).ServeHTTP(get, httptest.NewRequest(http.MethodGet, mountPrefix+faucetPath, nil))
	if get.Code != http.StatusNotFound {
		t.Errorf("GET on a gateway with no faucet = %d, want the same 404", get.Code)
	}
}

func TestFaucet_requestsItDoesNotUnderstandAreRefusedBeforeAnyDrip(t *testing.T) {
	good := `{"recipient":"` + faucetRecipient + `"`
	for name, tc := range map[string]struct {
		body string
		want string
	}{
		"not json":                  {`hello`, errBadRequest},
		"empty":                     {``, errBadRequest},
		"an unknown field":          {good + `,"count":9}`, errBadRequest},
		"a second object":           {good + `}` + good + `}`, errBadRequest},
		"an array":                  {`[]`, errBadRequest},
		"the amount as a number":    {good + `,"amount":5000000000}`, errBadRequest},
		"the recipient as a number": {`{"recipient":5}`, errBadRequest},
		"a zero amount":             {good + `,"amount":"0"}`, "bad_amount"},
		"a negative amount":         {good + `,"amount":"-5"}`, "bad_amount"},
		"a signed amount":           {good + `,"amount":"+5"}`, "bad_amount"},
		"a leading zero":            {good + `,"amount":"05"}`, "bad_amount"},
		"a decimal amount":          {good + `,"amount":"1.5"}`, "bad_amount"},
		"an exponent":               {good + `,"amount":"1e9"}`, "bad_amount"},
		"a hex amount":              {good + `,"amount":"0x10"}`, "bad_amount"},
		"a 19 digit amount":         {good + `,"amount":"` + strings.Repeat("9", 19) + `"}`, "bad_amount"},
		"an amount with a space":    {good + `,"amount":" 5"}`, "bad_amount"},
	} {
		t.Run(name, func(t *testing.T) {
			f := &fakeFaucet{}
			rec := postFaucet(faucetProxy(t, f), tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status %d: %s", rec.Code, rec.Body)
			}
			if got := faucetErrorOf(t, rec).Error; got != tc.want {
				t.Errorf("error = %q, want %q", got, tc.want)
			}
			if len(f.got()) != 0 {
				t.Error("a request that was refused reached the faucet")
			}
		})
	}
}

func TestFaucet_aTooLargeBodyIsRefused(t *testing.T) {
	f := &fakeFaucet{}
	rec := postFaucet(faucetProxy(t, f), `{"recipient":"`+strings.Repeat("a", faucetMaxBody)+`"}`)
	if rec.Code != http.StatusRequestEntityTooLarge || len(f.got()) != 0 {
		t.Fatalf("status %d, %d drips", rec.Code, len(f.got()))
	}
}

func TestFaucet_methodContentTypeAndQuery(t *testing.T) {
	p := faucetProxy(t, &fakeFaucet{})
	get := httptest.NewRecorder()
	p.ServeHTTP(get, httptest.NewRequest(http.MethodGet, mountPrefix+faucetPath, nil))
	if get.Code != http.StatusMethodNotAllowed || get.Header().Get("Allow") != http.MethodPost {
		t.Errorf("GET = %d, Allow %q", get.Code, get.Header().Get("Allow"))
	}
	text := httptest.NewRequest(http.MethodPost, mountPrefix+faucetPath, strings.NewReader(`{"recipient":"`+faucetRecipient+`"}`))
	text.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, text)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("text/plain = %d", rec.Code)
	}
	withQuery := httptest.NewRequest(http.MethodPost, mountPrefix+faucetPath+"?recipient=x", strings.NewReader(`{}`))
	withQuery.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	p.ServeHTTP(rec, withQuery)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("a query = %d", rec.Code)
	}
}

func TestFaucet_everyRefusalIsAStatusAndAKindTheClientCanActOn(t *testing.T) {
	for kind, status := range map[chainfaucet.Kind]int{
		chainfaucet.KindBadRecipient: http.StatusBadRequest,
		chainfaucet.KindBadAmount:    http.StatusBadRequest,
		chainfaucet.KindCooldown:     http.StatusTooManyRequests,
		chainfaucet.KindAllowance:    http.StatusTooManyRequests,
		chainfaucet.KindEpochCap:     http.StatusServiceUnavailable,
		chainfaucet.KindDisabled:     http.StatusForbidden,
		chainfaucet.KindBusy:         http.StatusServiceUnavailable,
		chainfaucet.KindUnavailable:  http.StatusServiceUnavailable,
		chainfaucet.KindPending:      http.StatusGatewayTimeout,
	} {
		t.Run(string(kind), func(t *testing.T) {
			f := &fakeFaucet{err: &chainfaucet.Refusal{Kind: kind, Message: "the reason"}}
			rec := postFaucet(faucetProxy(t, f), `{"recipient":"`+faucetRecipient+`"}`)
			if rec.Code != status {
				t.Fatalf("status %d, want %d: %s", rec.Code, status, rec.Body)
			}
			if e := faucetErrorOf(t, rec); e.Error != string(kind) || e.Message != "the reason" {
				t.Errorf("answer = %+v", e)
			}
			if kind == chainfaucet.KindBusy && rec.Header().Get("Retry-After") == "" {
				t.Error("a busy faucet gives no Retry-After")
			}
		})
	}
}

func TestFaucet_everyKindHasAStatus(t *testing.T) {
	for _, kind := range []chainfaucet.Kind{
		chainfaucet.KindBadRecipient, chainfaucet.KindBadAmount, chainfaucet.KindCooldown, chainfaucet.KindAllowance, chainfaucet.KindEpochCap,
		chainfaucet.KindDisabled, chainfaucet.KindBusy, chainfaucet.KindUnavailable, chainfaucet.KindPending,
	} {
		if _, ok := faucetStatus[kind]; !ok {
			t.Errorf("kind %s has no HTTP status", kind)
		}
	}
}

// What the faucet could not do against its chain is for its log: the requester is not told a path,
// an address or a node's text.
func TestFaucet_aFaultTellsTheRequesterNothingOfTheChain(t *testing.T) {
	for _, err := range []error{
		chainfaucet.ErrFault,
		errors.New("dial tcp 198.18.0.2:31003: connect: connection refused"),
		context.DeadlineExceeded,
	} {
		rec := postFaucet(faucetProxy(t, &fakeFaucet{err: err}), `{"recipient":"`+faucetRecipient+`"}`)
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("%v: status %d", err, rec.Code)
		}
		if e := faucetErrorOf(t, rec); e.Error != "faucet_failed" || strings.Contains(rec.Body.String(), "198.18") {
			t.Errorf("%v: answer %s", err, rec.Body)
		}
	}
}

func TestFaucet_requestsInFlightAreBounded(t *testing.T) {
	f := &fakeFaucet{block: make(chan struct{}), started: make(chan struct{}, faucetMaxConcurrent)}
	p := faucetProxy(t, f)
	done := make(chan int, faucetMaxConcurrent)
	for i := 0; i < faucetMaxConcurrent; i++ {
		go func() { done <- postFaucet(p, `{"recipient":"`+faucetRecipient+`"}`).Code }()
	}
	for i := 0; i < faucetMaxConcurrent; i++ {
		<-f.started
	}
	rec := postFaucet(p, `{"recipient":"`+faucetRecipient+`"}`)
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("a request over the bound = %d, Retry-After %q", rec.Code, rec.Header().Get("Retry-After"))
	}
	if e := faucetErrorOf(t, rec); e.Error != string(chainfaucet.KindBusy) {
		t.Errorf("answer = %+v", e)
	}
	close(f.block)
	for i := 0; i < faucetMaxConcurrent; i++ {
		if code := <-done; code != http.StatusOK {
			t.Errorf("a request within the bound = %d", code)
		}
	}
	if postFaucet(p, `{"recipient":"`+faucetRecipient+`"}`).Code != http.StatusOK {
		t.Error("the slots were not given back")
	}
}

// One client network cannot ask for the chain's maximum drip over and over for fresh recipients
// until the epoch's cap is spent for everyone: it has an allowance for the day.
func TestFaucet_aClientNetworkHasADailyAllowance(t *testing.T) {
	f := &fakeFaucet{}
	p := faucetProxy(t, f)
	each := big.NewInt(chainfaucet.DefaultBudgetNorama / 2)
	body := `{"recipient":"` + faucetRecipient + `","amount":"` + each.String() + `"}`
	for i := 0; i < 2; i++ {
		if rec := postFaucet(p, body); rec.Code != http.StatusOK {
			t.Fatalf("drip %d inside the allowance: %d %s", i, rec.Code, rec.Body)
		}
	}
	rec := postFaucet(p, body)
	if rec.Code != http.StatusTooManyRequests || faucetErrorOf(t, rec).Error != string(chainfaucet.KindAllowance) {
		t.Fatalf("a drip over the allowance: %d %s", rec.Code, rec.Body)
	}
	if secs, err := strconv.Atoi(rec.Header().Get("Retry-After")); err != nil || secs < 1 {
		t.Errorf("Retry-After = %q: it says when the allowance comes back", rec.Header().Get("Retry-After"))
	}
	if len(f.got()) != 2 {
		t.Errorf("%d drips reached the faucet; the refused one must not", len(f.got()))
	}
	other := httptest.NewRequest(http.MethodPost, mountPrefix+faucetPath, strings.NewReader(body))
	other.Header.Set("Content-Type", "application/json")
	other.RemoteAddr = "198.51.100.9:4000"
	otherRec := httptest.NewRecorder()
	p.ServeHTTP(otherRec, other)
	if otherRec.Code != http.StatusOK {
		t.Errorf("another network was refused for the first one's drips: %d %s", otherRec.Code, otherRec.Body)
	}
}

func TestFaucet_aDripThatWasNotMadeCostsNoAllowance(t *testing.T) {
	f := &fakeFaucet{err: &chainfaucet.Refusal{Kind: chainfaucet.KindCooldown, Message: "soon"}}
	p := faucetProxy(t, f)
	whole := big.NewInt(chainfaucet.DefaultBudgetNorama)
	body := `{"recipient":"` + faucetRecipient + `","amount":"` + whole.String() + `"}`
	for i := 0; i < 5; i++ {
		if rec := postFaucet(p, body); rec.Code != http.StatusTooManyRequests || faucetErrorOf(t, rec).Error != string(chainfaucet.KindCooldown) {
			t.Fatalf("refusal %d: %d %s; refusals of the chain must not use the allowance up", i, rec.Code, rec.Body)
		}
	}
	f.err = nil
	if rec := postFaucet(p, body); rec.Code != http.StatusOK {
		t.Errorf("a drip after five refusals: %d %s", rec.Code, rec.Body)
	}
}

// A drip that was sent and is not in a block yet may still land, so it stays charged.
func TestFaucet_aPendingDripStaysCharged(t *testing.T) {
	f := &fakeFaucet{err: &chainfaucet.Refusal{Kind: chainfaucet.KindPending, Message: "waiting"}}
	p := faucetProxy(t, f)
	whole := big.NewInt(chainfaucet.DefaultBudgetNorama)
	body := `{"recipient":"` + faucetRecipient + `","amount":"` + whole.String() + `"}`
	if rec := postFaucet(p, body); rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	f.err = nil
	if rec := postFaucet(p, body); rec.Code != http.StatusTooManyRequests || faucetErrorOf(t, rec).Error != string(chainfaucet.KindAllowance) {
		t.Errorf("a pending drip gave its allowance back: %d %s", rec.Code, rec.Body)
	}
}

// A caller that names its own network (a process on the node sends X-Forwarded-For as the proxy
// does) gets a fresh allowance for every name, so the gateway as a whole gives out only a ceiling.
func TestFaucet_theGatewayHasACeilingNoClientNameCanRaise(t *testing.T) {
	f := &fakeFaucet{}
	p := faucetProxy(t, f)
	each := big.NewInt(chainfaucet.DefaultBudgetNorama)
	body := `{"recipient":"` + faucetRecipient + `","amount":"` + each.String() + `"}`
	asClient := func(n int) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, mountPrefix+faucetPath, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "127.0.0.1:4000"
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("198.51.100.%d", n))
		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, req)
		return rec
	}
	granted := int(chainfaucet.DefaultCeilingNorama / chainfaucet.DefaultBudgetNorama)
	for n := 1; n <= granted; n++ {
		if rec := asClient(n); rec.Code != http.StatusOK {
			t.Fatalf("client %d inside the ceiling: %d %s", n, rec.Code, rec.Body)
		}
	}
	rec := asClient(granted + 1)
	if rec.Code != http.StatusTooManyRequests || faucetErrorOf(t, rec).Error != string(chainfaucet.KindAllowance) || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("a client past the ceiling: %d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(faucetErrorOf(t, rec).Message, "gateway") {
		t.Errorf("message %q does not say it is the gateway that has given out its share", faucetErrorOf(t, rec).Message)
	}
	if len(f.got()) != granted {
		t.Errorf("%d drips reached the faucet, want %d", len(f.got()), granted)
	}
}

// A drip that the client's own allowance refuses gives the ceiling back what it took.
func TestFaucet_aRefusalByTheAllowanceDoesNotSpendTheCeiling(t *testing.T) {
	f := &fakeFaucet{}
	p := faucetProxy(t, f)
	whole := big.NewInt(chainfaucet.DefaultBudgetNorama)
	body := `{"recipient":"` + faucetRecipient + `","amount":"` + whole.String() + `"}`
	if rec := postFaucet(p, body); rec.Code != http.StatusOK {
		t.Fatalf("the first drip: %d %s", rec.Code, rec.Body)
	}
	for i := 0; i < 50; i++ {
		if rec := postFaucet(p, body); rec.Code != http.StatusTooManyRequests {
			t.Fatalf("repeat %d: %d", i, rec.Code)
		}
	}
	other := httptest.NewRequest(http.MethodPost, mountPrefix+faucetPath, strings.NewReader(body))
	other.Header.Set("Content-Type", "application/json")
	other.RemoteAddr = "198.51.100.9:4000"
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, other)
	if rec.Code != http.StatusOK {
		t.Errorf("another network after 50 refusals: %d %s; the refused asks must not have used the ceiling up", rec.Code, rec.Body)
	}
}
