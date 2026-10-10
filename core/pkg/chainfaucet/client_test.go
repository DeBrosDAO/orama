package chainfaucet

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var clientHash = strings.Repeat("ab", 32)

// faucetGateway serves the faucet route with answer, and records the request it was sent.
func faucetGateway(t *testing.T, status int, answer string) (*httptest.Server, *map[string]string) {
	t.Helper()
	var got map[string]string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != RoutePath || r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(answer))
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func TestRequest_aDripIsReadBack(t *testing.T) {
	srv, got := faucetGateway(t, http.StatusOK, `{"tx_hash":"`+clientHash+`","amount":"5000000000","height":"77"}`)

	d, err := Request(context.Background(), srv.Client(), srv.URL, recipientN(t, 2), big.NewInt(5_000_000_000))

	if err != nil {
		t.Fatal(err)
	}
	if d.TxHash != strings.ToUpper(clientHash) || d.Height != 77 || d.Amount.Int64() != 5_000_000_000 {
		t.Errorf("dripped = %+v", d)
	}
	if (*got)["recipient"] != recipientN(t, 2) || (*got)["amount"] != "5000000000" {
		t.Errorf("the gateway was sent %v; the amount goes as a string", *got)
	}
}

func TestRequest_aTypedRefusalIsARefusalTheCallerCanActOn(t *testing.T) {
	srv, _ := faucetGateway(t, http.StatusTooManyRequests, `{"error":"cooldown","message":"faucet recipient is still within its cooldown\nnext drip at 99\u001b[2J"}`)

	_, err := Request(context.Background(), srv.Client(), srv.URL, recipientN(t, 2), big.NewInt(1))

	r := requireRefusal(t, err, KindCooldown)
	if strings.ContainsAny(r.Message, "\n\x1b") || !strings.Contains(r.Message, "next drip at 99") {
		t.Errorf("message = %q: the gateway's text is on one line and has no escapes", r.Message)
	}
}

func TestRequest_aGatewayWithNoFaucetIsNotARefusal(t *testing.T) {
	srv := httptest.NewTLSServer(http.NotFoundHandler())
	defer srv.Close()

	_, err := Request(context.Background(), srv.Client(), srv.URL, recipientN(t, 2), big.NewInt(1))

	var answer *ErrorAnswer
	if !errors.As(err, &answer) || answer.Status != http.StatusNotFound || !strings.Contains(err.Error(), "serves no faucet") {
		t.Fatalf("err = %v", err)
	}
}

// A gateway's answer is untrusted: one that says it paid something else, or nothing readable, is
// not taken as a drip.
func TestRequest_anAnswerThatIsNotTheDripAskedForIsRefused(t *testing.T) {
	for name, answer := range map[string]string{
		"another amount":     `{"tx_hash":"` + clientHash + `","amount":"1","height":"7"}`,
		"no hash":            `{"amount":"5","height":"7"}`,
		"a short hash":       `{"tx_hash":"abc","amount":"5","height":"7"}`,
		"a hash that is not": `{"tx_hash":"` + strings.Repeat("zz", 32) + `","amount":"5","height":"7"}`,
		"no height":          `{"tx_hash":"` + clientHash + `","amount":"5"}`,
		"a zero height":      `{"tx_hash":"` + clientHash + `","amount":"5","height":"0"}`,
		"not json":           `<html>`,
		"an empty body":      ``,
	} {
		t.Run(name, func(t *testing.T) {
			srv, _ := faucetGateway(t, http.StatusOK, answer)
			if _, err := Request(context.Background(), srv.Client(), srv.URL, recipientN(t, 2), big.NewInt(5)); err == nil {
				t.Fatal("the answer was taken as a drip")
			}
		})
	}
}

func TestRequest_otherFailuresNameTheGatewayAndAreCleaned(t *testing.T) {
	srv, _ := faucetGateway(t, http.StatusBadGateway, "boom\n\x1b[31mred")
	_, err := Request(context.Background(), srv.Client(), srv.URL, recipientN(t, 2), big.NewInt(1))
	var answer *ErrorAnswer
	if !errors.As(err, &answer) || answer.Status != http.StatusBadGateway || strings.ContainsAny(err.Error(), "\n\x1b") {
		t.Fatalf("err = %q", err)
	}
	if !strings.Contains(err.Error(), "boom | [31mred") {
		t.Errorf("err = %q, want the gateway's lines kept apart", err)
	}
}

func TestRequest_onlyAnHTTPSGatewayIsAsked(t *testing.T) {
	for _, base := range []string{
		"http://seed1.stagenet.orama.network",
		"ftp://seed1.example.org",
		"seed1.stagenet.orama.network",
		"https://user:pass@seed1.example.org",
		"https://seed1.example.org/some/path",
		"",
	} {
		if _, err := Request(context.Background(), http.DefaultClient, base, recipientN(t, 2), big.NewInt(1)); err == nil {
			t.Errorf("%q was asked", base)
		}
	}
	for _, loopback := range []string{"http://127.0.0.1:1", "http://localhost:1"} {
		_, err := Request(context.Background(), failingDoer{}, loopback, recipientN(t, 2), big.NewInt(1))
		if err == nil || !strings.Contains(err.Error(), "unreachable") {
			t.Errorf("%s: err = %v, want the request to go out", loopback, err)
		}
	}
}

type failingDoer struct{}

func (failingDoer) Do(*http.Request) (*http.Response, error) { return nil, errors.New("unreachable") }

func TestRequest_aGatewayThatCannotBeReachedIsAnErrorThatNamesIt(t *testing.T) {
	_, err := Request(context.Background(), failingDoer{}, "https://seed1.stagenet.orama.network", recipientN(t, 2), big.NewInt(1))
	if err == nil || !strings.Contains(err.Error(), "seed1.stagenet.orama.network") {
		t.Fatalf("err = %v", err)
	}
}

func TestRequest_anOversizedAnswerIsNotTrusted(t *testing.T) {
	srv, _ := faucetGateway(t, http.StatusOK, strings.Repeat("a", maxAnswerBytes+1))
	if _, err := Request(context.Background(), srv.Client(), srv.URL, recipientN(t, 2), big.NewInt(1)); err == nil {
		t.Fatal("an oversized answer was read")
	}
}
