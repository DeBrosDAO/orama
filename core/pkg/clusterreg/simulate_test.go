package clusterreg

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchBaseFee_readsTheQuery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/orama/fees/v1/base-fee" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"base_fee":"25"}`))
	}))
	defer srv.Close()
	fee, err := FetchBaseFee(context.Background(), srv.URL+"/")
	if err != nil || fee != "25" {
		t.Fatalf("FetchBaseFee = %q, %v", fee, err)
	}
}

func TestFetchBaseFee_refusesWhatIsNotAnInteger(t *testing.T) {
	for name, body := range map[string]string{
		"decimal":  `{"base_fee":"0.5"}`,
		"empty":    `{"base_fee":""}`,
		"negative": `{"base_fee":"-1"}`,
		"missing":  `{}`,
		"not json": `x`,
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer srv.Close()
			if _, err := FetchBaseFee(context.Background(), srv.URL); err == nil {
				t.Fatal("FetchBaseFee accepted it")
			}
		})
	}
}

func TestSimulateGas_returnsGasUsed(t *testing.T) {
	var posted string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		posted = r.URL.Path + " " + string(body)
		_, _ = w.Write([]byte(`{"gas_info":{"gas_wanted":"200000","gas_used":"91234"},"result":{}}`))
	}))
	defer srv.Close()
	used, err := SimulateGas(context.Background(), srv.URL, []byte{1, 2, 3})
	if err != nil || used != 91234 {
		t.Fatalf("SimulateGas = %d, %v", used, err)
	}
	if posted != `/cosmos/tx/v1beta1/simulate {"tx_bytes":"AQID"}` {
		t.Errorf("posted %q", posted)
	}
}

func TestSimulateGas_carriesTheChainsReason(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"code":2,"message":"operator already registered\u001b[31m: unauthorized"}`))
	}))
	defer srv.Close()
	_, err := SimulateGas(context.Background(), srv.URL, []byte{1})
	if err == nil || !strings.Contains(err.Error(), "operator already registered[31m: unauthorized") {
		t.Fatalf("SimulateGas = %v, want the chain's reason with control characters removed", err)
	}
	var status *StatusError
	if !errors.As(err, &status) || status.Code != http.StatusInternalServerError {
		t.Errorf("the status error is not kept: %v", err)
	}
}

func TestSimulateGas_noGasUsedIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"gas_info":{"gas_used":"0"}}`))
	}))
	defer srv.Close()
	if _, err := SimulateGas(context.Background(), srv.URL, []byte{1}); err == nil {
		t.Fatal("a simulation that used no gas was accepted")
	}
}

func TestErrorMessage_cutsAndCleans(t *testing.T) {
	long := strings.Repeat("x", maxErrorMessage*2)
	if got := errorMessage([]byte(long)); len([]rune(got)) != maxErrorMessage {
		t.Errorf("a long body kept %d characters", len([]rune(got)))
	}
	if got := errorMessage([]byte("plain\ntext")); got != "plaintext" {
		t.Errorf("errorMessage = %q", got)
	}
}
