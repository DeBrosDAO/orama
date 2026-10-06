package indexer

import (
	"fmt"

	abci "github.com/cometbft/cometbft/abci/types"
	gogoproto "github.com/cosmos/gogoproto/proto"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/bech32"
	txtypes "github.com/cosmos/cosmos-sdk/types/tx"

	"github.com/DeBrosOfficial/network/chain/app/params"
)

const (
	// maxAddressLen is the SDK's bech32 decode limit on an encoded string.
	maxAddressLen = 1023
	// maxAddressBytes is the SDK's limit on an address (address.MaxAddrLen).
	maxAddressBytes = 255
)

// bodyMessages decodes the messages of a raw transaction without resolving
// their types, so a message from any module still yields its type URL.
func bodyMessages(raw []byte) ([]*codectypes.Any, error) {
	var txRaw txtypes.TxRaw
	if err := gogoproto.Unmarshal(raw, &txRaw); err != nil {
		return nil, fmt.Errorf("decode tx envelope: %w", err)
	}
	var body txtypes.TxBody
	if err := gogoproto.Unmarshal(txRaw.BodyBytes, &body); err != nil {
		return nil, fmt.Errorf("decode tx body: %w", err)
	}
	return body.Messages, nil
}

// msgResponses decodes a successful transaction's result data: one response
// per message, in message order.
func msgResponses(data []byte) ([]*codectypes.Any, error) {
	var msgData sdk.TxMsgData
	if err := gogoproto.Unmarshal(data, &msgData); err != nil {
		return nil, fmt.Errorf("decode tx result data: %w", err)
	}
	return msgData.MsgResponses, nil
}

func typeURLs(msgs []*codectypes.Any) []string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, m.TypeUrl)
	}
	return out
}

func toEvents(in []abci.Event) []Event {
	out := make([]Event, 0, len(in))
	for _, e := range in {
		attrs := make([]Attribute, 0, len(e.Attributes))
		for _, a := range e.Attributes {
			attrs = append(attrs, Attribute{Key: a.Key, Value: a.Value})
		}
		out = append(out, Event{Type: e.Type, Attributes: attrs})
	}
	return out
}

// eventAddresses returns each distinct account address that is the whole
// value of an event attribute, in first-seen order. Only the account prefix
// counts: valoper and valcons addresses are other bech32 prefixes.
func eventAddresses(events []abci.Event) []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range events {
		for _, a := range e.Attributes {
			if seen[a.Value] || !IsAccountAddress(a.Value) {
				continue
			}
			seen[a.Value] = true
			out = append(out, a.Value)
		}
	}
	return out
}

// IsAccountAddress reports whether s is a canonical (lowercase) bech32 Orama
// account address.
func IsAccountAddress(s string) bool {
	canonical, _, err := canonicalAccount(s)
	return err == nil && canonical == s
}

// canonicalAccount decodes an account address the way the SDK's
// AccAddressFromBech32 accepts it (either single case, 1 to 255 bytes) and
// returns its lowercase encoding and bytes. The chain stores and hashes the
// bytes, so the index keys owners by the canonical string.
func canonicalAccount(s string) (string, []byte, error) {
	if len(s) == 0 || len(s) > maxAddressLen {
		return "", nil, fmt.Errorf("address of %d characters is not an account address", len(s))
	}
	hrp, raw, err := bech32.DecodeAndConvert(s)
	if err != nil {
		return "", nil, fmt.Errorf("address %q: %w", s, err)
	}
	if hrp != params.Bech32Prefix || len(raw) == 0 || len(raw) > maxAddressBytes {
		return "", nil, fmt.Errorf("address %q is not an orama account address", s)
	}
	canonical, err := bech32.ConvertAndEncode(hrp, raw)
	if err != nil {
		return "", nil, fmt.Errorf("address %q: %w", s, err)
	}
	return canonical, raw, nil
}
