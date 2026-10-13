//go:build e2e_fleet

package chain

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// Msg is one transaction message in proto-JSON: "@type" is its type URL
// (e.g. "/orama.nodes.v1.MsgRegisterOperator"), the other members its fields
// by proto name. Integers of 64 bits and math.Int amounts are strings,
// bytes are base64, enums are their names.
type Msg map[string]any

// NewMsg builds a Msg of typeURL with fields.
func NewMsg(typeURL string, fields map[string]any) Msg {
	m := Msg{"@type": typeURL}
	for k, v := range fields {
		m[k] = v
	}
	return m
}

// DefaultGas is the gas limit of a transaction that does not choose one:
// comfortably above what one module message consumes, and far under the
// run chain's block max_gas of 100,000,000 (chain-deploy.sh). At the base fee
// floor of 1 norama/gas (x/fees DefaultParams) it costs 0.0006 ORAMA.
const DefaultGas = 600_000

// FeeMode says how the fee is computed on the node, from the base fee read
// inside the key's lock, right before signing (a validator key has no bank
// balance, so any norama above base_fee*gas is a tip it cannot pay:
// x/fees/keeper/feepay.go SettleFee).
type FeeMode string

const (
	// FeeExact pays base_fee*gas + FeeDelta (FeeDelta 0: exactly the base fee).
	FeeExact FeeMode = "delta"
	// FeeAbsolute pays FeeAmount norama whatever the base fee.
	FeeAbsolute FeeMode = "abs"
)

// TxOptions shapes one transaction. The zero value is a transaction paying
// exactly the base fee with DefaultGas, signed for the run's chain id with
// the account's live number and sequence.
type TxOptions struct {
	Gas       uint64
	Mode      FeeMode
	FeeDelta  int64
	FeeAmount string
	Granter   string
	Memo      string
	// ChainID overrides the chain id the signature commits to.
	ChainID string
	// Offline signs with AccountNumber/Sequence instead of the live ones.
	Offline       bool
	AccountNumber uint64
	Sequence      uint64
	TimeoutHeight uint64
}

func (o TxOptions) mode() FeeMode {
	if o.Mode == "" {
		return FeeExact
	}
	return o.Mode
}

func (o TxOptions) gas() uint64 {
	if o.Gas == 0 {
		return DefaultGas
	}
	return o.Gas
}

// unsignedTx is the proto-JSON of a cosmos.tx.v1beta1.Tx with no signature.
// The fee amount is a placeholder the node overwrites (script.go).
func unsignedTx(opts TxOptions, msgs []Msg) ([]byte, error) {
	if len(msgs) == 0 {
		return nil, fmt.Errorf("a transaction needs at least one message")
	}
	tx := map[string]any{
		"body": map[string]any{
			"messages":                       msgs,
			"memo":                           opts.Memo,
			"timeout_height":                 fmt.Sprint(opts.TimeoutHeight),
			"extension_options":              []any{},
			"non_critical_extension_options": []any{},
		},
		"auth_info": map[string]any{
			"signer_infos": []any{},
			"fee": map[string]any{
				"amount":    []any{},
				"gas_limit": fmt.Sprint(opts.gas()),
				"payer":     "",
				"granter":   opts.Granter,
			},
		},
		"signatures": []any{},
	}
	raw, err := json.Marshal(tx)
	if err != nil {
		return nil, fmt.Errorf("failed to encode the unsigned transaction: %w", err)
	}
	return raw, nil
}

// Event is one ABCI event of a delivered transaction.
type Event struct {
	Type       string `json:"type"`
	Attributes []struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	} `json:"attributes"`
}

// Attr returns the first value of key in the first event of type typ.
func Attr(events []Event, typ, key string) (string, bool) {
	for _, e := range events {
		if e.Type != typ {
			continue
		}
		for _, a := range e.Attributes {
			if a.Key == key {
				return a.Value, true
			}
		}
	}
	return "", false
}

// txResponse is cosmos.base.abci.v1beta1.TxResponse as the CLI prints it.
type txResponse struct {
	Height    Int     `json:"height"`
	TxHash    string  `json:"txhash"`
	Codespace string  `json:"codespace"`
	Code      uint32  `json:"code"`
	RawLog    string  `json:"raw_log"`
	GasWanted Int     `json:"gas_wanted"`
	GasUsed   Int     `json:"gas_used"`
	Events    []Event `json:"events"`
	Data      string  `json:"data"`
}

// Stage says where a transaction ended.
type Stage string

const (
	// StageRPC: the RPC refused the broadcast itself (e.g. already in the cache).
	StageRPC Stage = "rpc"
	// StageCheck: CheckTx refused it (ante, ValidateBasic); never in a block.
	StageCheck Stage = "check"
	// StageBlock: it is in a block, successful (Code 0) or failed.
	StageBlock Stage = "block"
	// StageNotIncluded: accepted by CheckTx but not in a block in time.
	StageNotIncluded Stage = "not-included"
)

// Result is what happened to one broadcast transaction.
type Result struct {
	Stage     Stage
	TxHash    string
	Code      uint32
	Codespace string
	Log       string
	Height    int64
	GasUsed   int64
	GasWanted int64
	Events    []Event
	// Signed is the exact signed transaction (proto-JSON) that was broadcast.
	Signed []byte
	// Data is the delivered transaction's TxMsgData, hex as the CLI prints it.
	Data string
}

// MsgResponses decodes the delivered transaction's message responses
// (cosmos.base.abci.v1beta1.TxMsgData.msg_responses, each an Any whose value
// is the Msg...Response), one Fields per message.
func (r Result) MsgResponses() ([]Fields, error) {
	raw, err := hex.DecodeString(r.Data)
	if err != nil {
		return nil, fmt.Errorf("transaction data is not hex: %w", err)
	}
	data, err := DecodePB(raw)
	if err != nil {
		return nil, fmt.Errorf("transaction data: %w", err)
	}
	var out []Fields
	for _, v := range data[txMsgDataResponsesField] {
		b, _ := v.([]byte)
		anyMsg, err := DecodePB(b)
		if err != nil {
			return nil, fmt.Errorf("msg response: %w", err)
		}
		inner, ok := anyMsg.Msg(anyValueField)
		if !ok {
			inner = Fields{}
		}
		out = append(out, inner)
	}
	return out, nil
}

// Field numbers of TxMsgData.msg_responses and google.protobuf.Any.value.
const (
	txMsgDataResponsesField = 2
	anyValueField           = 2
)

// OK reports a transaction delivered in a block with code 0.
func (r Result) OK() bool { return r.Stage == StageBlock && r.Code == 0 }

func (r Result) String() string {
	return fmt.Sprintf("stage=%s code=%d codespace=%q height=%d hash=%s log=%q", r.Stage, r.Code, r.Codespace, r.Height, r.TxHash, r.Log)
}

// RequireOK fails the test unless r was delivered with code 0.
func RequireOK(t testing.TB, what string, r Result) Result {
	t.Helper()
	if !r.OK() {
		t.Fatalf("%s: want a successful transaction, got %s", what, r)
	}
	return r
}

// RequireRefused fails the test unless r was refused (at any stage) with a
// log containing every one of wants. It returns r for further checks.
func RequireRefused(t testing.TB, what string, r Result, wants ...string) Result {
	t.Helper()
	if r.OK() {
		t.Fatalf("%s: want a refusal, the transaction succeeded at height %d", what, r.Height)
	}
	if r.Stage == StageNotIncluded {
		t.Fatalf("%s: want a refusal, the transaction was accepted but never included: %s", what, r)
	}
	for _, w := range wants {
		if !strings.Contains(r.Log, w) {
			t.Fatalf("%s: refusal log does not mention %q: %s", what, w, r)
		}
	}
	return r
}

// RequireCode is RequireRefused that also pins the ABCI codespace and code.
func RequireCode(t testing.TB, what string, r Result, codespace string, code uint32, wants ...string) Result {
	t.Helper()
	RequireRefused(t, what, r, wants...)
	if r.Codespace != codespace || r.Code != code {
		t.Fatalf("%s: want %s/%d, got %s", what, codespace, code, r)
	}
	return r
}
