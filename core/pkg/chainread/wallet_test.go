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
func TestDecodeRPC_outOfGasIsErrOutOfGas(t *testing.T) {
	m, err := Lookup("orama.nodes.v1.Query/Nodes")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.DecodeRPC([]byte(`{"result":{"response":{"code":11,"codespace":"sdk","log":"out of gas"}}}`)); !errors.Is(err, ErrOutOfGas) {
		t.Fatalf("err = %v, want ErrOutOfGas", err)
	}
	if _, err := m.DecodeRPC([]byte(`{"result":{"response":{"code":11,"codespace":"wasm","log":"x"}}}`)); errors.Is(err, ErrOutOfGas) {
		t.Fatal("another codespace's code 11 was classed out of gas")
	}
}

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

// The same wasmd error reaches the explorer through AllContractState (an address that is no
// contract) and CodeInfo (a code id that is not stored, ErrNoSuchCode); each is a 404 for its own
// phrase, and the other phrase on the wrong method is not.
func TestDecodeRPC_noSuchContractAndNoSuchCodeAreErrNotFoundForTheirQueries(t *testing.T) {
	contract := []byte(`{"result":{"response":{"code":6,"codespace":"sdk","log":"no such contract: address orama1qq: unknown request"}}}`)
	code := []byte(`{"result":{"response":{"code":6,"codespace":"sdk","log":"no such code: code id 9: unknown request"}}}`)
	for name, tc := range map[string]struct {
		method string
		body   []byte
		want   bool
	}{
		"state of a non-contract":    {"cosmwasm.wasm.v1.Query/AllContractState", contract, true},
		"info of a missing code":     {"cosmwasm.wasm.v1.Query/CodeInfo", code, true},
		"code phrase on contract":    {"cosmwasm.wasm.v1.Query/ContractInfo", code, false},
		"contract phrase on code":    {"cosmwasm.wasm.v1.Query/CodeInfo", contract, false},
		"contract phrase on history": {"cosmwasm.wasm.v1.Query/ContractHistory", contract, false},
	} {
		m, err := Lookup(tc.method)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		_, err = m.DecodeRPC(tc.body)
		if got := errors.Is(err, ErrNotFound); got != tc.want {
			t.Errorf("%s: not found = %v (err %v), want %v", name, got, err, tc.want)
		}
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

func TestPage(t *testing.T) {
	m, err := Lookup("cosmos.bank.v1beta1.Query/AllBalances")
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		req  string
		want Page
	}{
		"no pagination": {`{"address":"orama1x"}`, Page{}},
		"a total count": {`{"address":"orama1x","pagination":{"count_total":true}}`, Page{CountTotal: true}},
		"a limit":       {`{"address":"orama1x","pagination":{"limit":42}}`, Page{Limit: 42}},
		"an offset":     {`{"address":"orama1x","pagination":{"offset":7,"limit":5}}`, Page{Limit: 5, Offset: 7}},
		"a huge limit":  {`{"pagination":{"limit":"18446744073709551615"}}`, Page{Limit: 18446744073709551615}},
	} {
		data, err := m.EncodeRequest(tc.req)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got, err := m.Page(data); err != nil || got != tc.want {
			t.Errorf("%s: page %+v err %v, want %+v", name, got, err, tc.want)
		}
	}
	if _, err := m.Page([]byte{0xff}); err == nil {
		t.Error("a malformed request has a page")
	}
	node, _ := Lookup("orama.nodes.v1.Query/Node")
	if got, err := node.Page(nil); err != nil || got != (Page{}) {
		t.Errorf("a query without pagination: page %+v err %v", got, err)
	}
}
