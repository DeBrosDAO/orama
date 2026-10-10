package chainreach

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestParseChainNode(t *testing.T) {
	raw := json.RawMessage(`{"node":{"node_id":"n1","operator":"orama1abc","status":"NODE_STATUS_ACTIVE",` +
		`"declared_capacity_bytes":"500000000000","reserved_capacity_bytes":"1000","endpoints":["203.0.113.9:4001"]}}`)

	got, err := parseChainNode(raw)

	if err != nil {
		t.Fatalf("parseChainNode: %v", err)
	}
	want := ChainNode{ID: "n1", Operator: "orama1abc", Status: "NODE_STATUS_ACTIVE", DeclaredBytes: 500_000_000_000, ReservedBytes: 1000, Endpoints: []string{"203.0.113.9:4001"}}
	if !reflect.DeepEqual(*got, want) {
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

func TestChainNode_ListsHost(t *testing.T) {
	tests := []struct {
		name          string
		endpoints     []string
		host          string
		listed, known bool
	}{
		{"host and port", []string{"203.0.113.9:4001"}, "203.0.113.9", true, true},
		{"one of several", []string{"198.51.100.2:31000", "203.0.113.9:31010"}, "203.0.113.9", true, true},
		{"a URL", []string{"https://203.0.113.9:4001/path"}, "203.0.113.9", true, true},
		{"a multiaddr", []string{"/ip4/203.0.113.9/tcp/4001"}, "203.0.113.9", true, true},
		{"a bare address", []string{"203.0.113.9"}, "203.0.113.9", true, true},
		{"another machine", []string{"198.51.100.2:4001"}, "203.0.113.9", false, true},
		{"an address that only contains the host", []string{"1203.0.113.99:4001"}, "203.0.113.9", false, false},
		{"the host only in a path", []string{"https://evil.example/203.0.113.9"}, "203.0.113.9", false, false},
		{"hostnames only cannot be compared", []string{"node.example:443", "/dns4/node.example/tcp/443"}, "203.0.113.9", false, false},
		{"an IPv6 endpoint cannot be compared", []string{"[2001:db8::1]:443"}, "203.0.113.9", false, false},
		{"a hostname beside a literal that is another machine", []string{"node.example:443", "198.51.100.2:443"}, "203.0.113.9", false, true},
		{"no endpoint registered", nil, "203.0.113.9", false, false},
		{"a target that is not an IPv4 address", []string{"203.0.113.9:4001"}, "node.example", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			listed, known := ChainNode{Endpoints: tt.endpoints}.ListsHost(tt.host)
			if listed != tt.listed || known != tt.known {
				t.Errorf("ListsHost(%q) = %v, %v; want %v, %v", tt.host, listed, known, tt.listed, tt.known)
			}
		})
	}
}
