package indexer

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
)

// The filters of the transaction lists. A transaction is listed under "all" and under each other
// filter it matches: "failed" when the chain refused it, and one of the kinds for each kind of
// message it carries.
const (
	FilterAll       = "all"
	FilterFailed    = "failed"
	FilterTransfers = "transfers"
	FilterStaking   = "staking"
	FilterStorage   = "storage"
)

// listFilters are the values the filter parameter takes.
var listFilters = []string{FilterAll, FilterFailed, FilterTransfers, FilterStaking, FilterStorage}

// messageKindsJSON maps a message type URL to the kind the explorer shows it under (transfers,
// staking or storage); a type it does not name is no kind. The explorer's decoders define the
// kinds and a test there holds this file to them (website/src/explorer/data/chain/decoders/kinds.test.ts).
//
//go:embed message_kinds.json
var messageKindsJSON []byte

var messageKinds = mustLoadKinds(messageKindsJSON)

func mustLoadKinds(raw []byte) map[string]string {
	kinds := map[string]string{}
	if err := json.Unmarshal(raw, &kinds); err != nil {
		panic(fmt.Sprintf("indexer: message_kinds.json is not a JSON object of strings: %v", err))
	}
	for url, kind := range kinds {
		if kind == FilterAll || kind == FilterFailed || !slices.Contains(listFilters, kind) {
			panic(fmt.Sprintf("indexer: message_kinds.json gives %s the kind %q, which is not a message kind", url, kind))
		}
	}
	return kinds
}

// IsListFilter reports whether s is a value of the filter parameter.
func IsListFilter(s string) bool { return slices.Contains(listFilters, s) }

// kindsOf returns the filters, besides "all", a transaction is listed under, each once.
func kindsOf(messages []string, failed bool) []string {
	var out []string
	if failed {
		out = append(out, FilterFailed)
	}
	for _, url := range messages {
		kind, ok := messageKinds[url]
		if ok && !slices.Contains(out, kind) {
			out = append(out, kind)
		}
	}
	return out
}
