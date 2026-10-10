package chainreach

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseChainNode(t *testing.T) {
	raw := json.RawMessage(`{"node":{"node_id":"n1","operator":"orama1abc","status":"NODE_STATUS_ACTIVE",` +
		`"declared_capacity_bytes":"500000000000","reserved_capacity_bytes":"1000"}}`)

	got, err := parseChainNode(raw)

	if err != nil {
		t.Fatalf("parseChainNode: %v", err)
	}
	want := ChainNode{ID: "n1", Operator: "orama1abc", Status: "NODE_STATUS_ACTIVE", DeclaredBytes: 500_000_000_000, ReservedBytes: 1000}
	if *got != want {
		t.Errorf("node = %+v, want %+v", *got, want)
	}
	if got.Gone() {
		t.Error("an active node has not left the chain")
	}
}

func TestParseChainNode_zeroBytesAreOmittedByProtojson(t *testing.T) {
	got, err := parseChainNode(json.RawMessage(`{"node":{"node_id":"n1","status":"NODE_STATUS_REGISTERED"}}`))

	if err != nil || got.DeclaredBytes != 0 || got.ReservedBytes != 0 {
		t.Fatalf("node = %+v, %v; want zero capacity", got, err)
	}
}

func TestParseChainNode_aBadByteCountIsAnError(t *testing.T) {
	_, err := parseChainNode(json.RawMessage(`{"node":{"declared_capacity_bytes":"lots"}}`))

	if err == nil || !strings.Contains(err.Error(), "declared_capacity_bytes") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseChainNode_notJSON(t *testing.T) {
	if _, err := parseChainNode(json.RawMessage(`[1]`)); err == nil {
		t.Fatal("a non-object answer must be an error")
	}
}

func TestChainNode_Gone(t *testing.T) {
	for status, want := range map[string]bool{
		StatusRetired: true, StatusTombstoned: true, "NODE_STATUS_ACTIVE": false, "NODE_STATUS_JAILED": false, "": false,
	} {
		if got := (ChainNode{Status: status}).Gone(); got != want {
			t.Errorf("Gone(%q) = %v, want %v", status, got, want)
		}
	}
}
