package chainread

import (
	"errors"
	"testing"
)

func TestSDKMethods_listsTheWalletServices(t *testing.T) {
	methods, err := SDKMethods()
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, m := range methods {
		have[m] = true
	}
	for _, want := range []string{
		"cosmos.bank.v1beta1.Query/Balance",
		"cosmos.bank.v1beta1.Query/AllBalances",
		"cosmos.auth.v1beta1.Query/AccountInfo",
		"cosmos.staking.v1beta1.Query/DelegatorDelegations",
		"cosmos.distribution.v1beta1.Query/DelegationTotalRewards",
		"cosmwasm.wasm.v1.Query/ContractInfo",
	} {
		if !have[want] {
			t.Errorf("%s is missing from the embedded descriptors (run gen.sh)", want)
		}
	}
	orama, err := Methods()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range orama {
		if have[m] {
			t.Errorf("%s is in both Methods and SDKMethods", m)
		}
	}
}

// wasmd returns ErrNoSuchContract for an address that is no contract, and baseapp reports any error
// that is not a gRPC status as code 6 of codespace sdk: the wasm code is gone (bug #739).
func TestDecodeRPC_contractInfoOfANonContractIsErrNotFound(t *testing.T) {
	m, err := Lookup("cosmwasm.wasm.v1.Query/ContractInfo")
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"result":{"response":{"code":6,"codespace":"sdk","log":"no such contract: address orama1qq: unknown request"}}}`)
	if _, err := m.DecodeRPC(body); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestDecodeRPC_noSuchContractIsOnlyANotFoundForContractInfo(t *testing.T) {
	body := []byte(`{"result":{"response":{"code":6,"codespace":"sdk","log":"no such contract: unknown request"}}}`)
	m, err := Lookup("cosmos.bank.v1beta1.Query/Balance")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.DecodeRPC(body); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("a bank query was classed not found: %v", err)
	}
	info, _ := Lookup("cosmwasm.wasm.v1.Query/ContractInfo")
	for name, doc := range map[string]string{
		"another message":   `{"result":{"response":{"code":6,"codespace":"sdk","log":"unknown request"}}}`,
		"another code":      `{"result":{"response":{"code":5,"codespace":"sdk","log":"no such contract"}}}`,
		"another codespace": `{"result":{"response":{"code":6,"codespace":"wasm","log":"no such contract"}}}`,
		"a bad value":       `{"result":{"response":{"code":0,"value":"!!"}}}`,
	} {
		if _, err := info.DecodeRPC([]byte(doc)); err == nil || errors.Is(err, ErrNotFound) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestDecodeRPC_invalidRequestIsErrInvalidRequest(t *testing.T) {
	m, _ := Lookup("cosmos.bank.v1beta1.Query/Balance")
	body := []byte(`{"result":{"response":{"code":18,"codespace":"sdk","log":"decoding bech32 failed"}}}`)
	if _, err := m.DecodeRPC(body); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("err = %v", err)
	}
	other := []byte(`{"result":{"response":{"code":18,"codespace":"wasm","log":"x"}}}`)
	if _, err := m.DecodeRPC(other); errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("another codespace's code 18 was classed invalid request")
	}
}

func TestPageLimit(t *testing.T) {
	m, err := Lookup("cosmos.bank.v1beta1.Query/AllBalances")
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		req  string
		want uint64
	}{
		"no pagination": {`{"address":"orama1x"}`, 0},
		"unset limit":   {`{"address":"orama1x","pagination":{"count_total":true}}`, 0},
		"a limit":       {`{"address":"orama1x","pagination":{"limit":42}}`, 42},
		"a huge limit":  {`{"pagination":{"limit":"18446744073709551615"}}`, 18446744073709551615},
	} {
		data, err := m.EncodeRequest(tc.req)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got, err := m.PageLimit(data); err != nil || got != tc.want {
			t.Errorf("%s: limit %d err %v, want %d", name, got, err, tc.want)
		}
	}
	if _, err := m.PageLimit([]byte{0xff}); err == nil {
		t.Error("a malformed request has a page limit")
	}
	node, _ := Lookup("orama.nodes.v1.Query/Node")
	if got, err := node.PageLimit(nil); err != nil || got != 0 {
		t.Errorf("a query without pagination: limit %d err %v", got, err)
	}
}
