package chainread

import (
	"bytes"
	"encoding/json"
	"testing"
)

// FuzzChainreadQuery: the gateway turns a caller's JSON into a protobuf query, and a node's bytes
// into JSON. For any method of the embedded descriptors and any input, EncodeRequest,
// CheckRequest, Page, DecodeResponse and DecodeRPC never panic; a request that encodes is one
// CheckRequest accepts, encodes the same twice and reads a pagination back; a response that decodes
// is valid JSON; and the abci_query envelope never turns into a result when it carries an error
// code.
func FuzzChainreadQuery(f *testing.F) {
	methods, err := Methods()
	if err != nil {
		f.Fatal(err)
	}
	if len(methods) == 0 {
		f.Fatal("no query methods")
	}
	f.Add(uint16(0), `{}`, []byte{}, []byte(`{"jsonrpc":"2.0","id":1,"result":{"response":{"code":0,"value":"CgA="}}}`))
	f.Add(uint16(1), `{"pagination":{"limit":"5","offset":"2","count_total":true}}`, []byte{0x0a, 0x00}, []byte(`{"jsonrpc":"2.0","id":1,"result":{"response":{"code":18,"codespace":"sdk","log":"x"}}}`))
	f.Add(uint16(2), `{"id":"1"}`, []byte{0xff, 0xff}, []byte(`{"error":{"code":-1,"message":"m"}}`))
	f.Add(uint16(3), `null`, []byte(nil), []byte(`[]`))
	f.Fuzz(func(t *testing.T, index uint16, requestJSON string, response, rpcBody []byte) {
		name := methods[int(index)%len(methods)]
		m, err := Lookup(name)
		if err != nil {
			t.Fatalf("Lookup(%q) of a listed method: %v", name, err)
		}
		data, err := m.EncodeRequest(requestJSON)
		if err == nil {
			if err := m.CheckRequest(data); err != nil {
				t.Fatalf("a request EncodeRequest wrote is refused by CheckRequest: %v", err)
			}
			again, _ := m.EncodeRequest(requestJSON)
			if !bytes.Equal(again, data) {
				t.Fatal("EncodeRequest is not deterministic")
			}
			if _, err := m.Page(data); err != nil {
				t.Fatalf("Page refuses a request CheckRequest accepts: %v", err)
			}
		}
		_ = m.CheckRequest(response)
		_, _ = m.Page(response)
		if out, err := m.DecodeResponse(response); err == nil && !json.Valid(out) {
			t.Fatalf("a decoded response is not JSON: %q", out)
		}
		if out, err := m.DecodeRPC(rpcBody); err == nil {
			if !json.Valid(out) {
				t.Fatalf("a decoded RPC answer is not JSON: %q", out)
			}
			var env struct {
				Result struct {
					Response struct {
						Code uint32 `json:"code"`
					} `json:"response"`
				} `json:"result"`
			}
			if json.Unmarshal(rpcBody, &env) == nil && env.Result.Response.Code != 0 {
				t.Fatalf("an answer with ABCI code %d became a result", env.Result.Response.Code)
			}
		}
	})
}
