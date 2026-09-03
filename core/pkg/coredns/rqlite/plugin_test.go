package rqlite

import (
	"reflect"
	"testing"

	"go.uber.org/zap"
)

func wildcardPlugin(zones ...string) *RQLitePlugin {
	return &RQLitePlugin{zones: zones, logger: zap.NewNop()}
}

func TestWildcardCandidates_walksOutward(t *testing.T) {
	p := wildcardPlugin("orama-devnet.network.")

	tests := []struct {
		name  string
		qname string
		want  []string
	}{
		{
			// The reproduction. This used to produce
			// "*.ns-anchat.orama-devnet." — the TLD dropped — so it matched
			// none of the *.ns-<ns>.<base>. rows and every per-namespace
			// sub-name was unresolvable.
			name:  "four labels",
			qname: "x.ns-anchat.orama-devnet.network.",
			want: []string{
				"*.ns-anchat.orama-devnet.network.",
				"*.orama-devnet.network.",
				"*.network.",
			},
		},
		{
			name:  "three labels",
			qname: "ns-anchat.orama-devnet.network.",
			want: []string{
				"*.orama-devnet.network.",
				"*.network.",
			},
		},
		{
			name:  "five labels",
			qname: "a.b.ns-anchat.orama-devnet.network.",
			want: []string{
				"*.b.ns-anchat.orama-devnet.network.",
				"*.ns-anchat.orama-devnet.network.",
				"*.orama-devnet.network.",
				"*.network.",
			},
		},
		{name: "two labels", qname: "orama-devnet.network.", want: []string{"*.network."}},
		{name: "one label", qname: "network.", want: nil},
		{name: "root", qname: ".", want: nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := p.wildcardCandidates(tc.qname)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWildcardCandidates_mostSpecificFirst(t *testing.T) {
	// Order matters: the first match wins, and a more specific wildcard must
	// beat a broader one.
	p := wildcardPlugin("orama-devnet.network.")
	got := p.wildcardCandidates("turn.ns-anchat.orama-devnet.network.")

	if len(got) == 0 {
		t.Fatal("no candidates")
	}
	if got[0] != "*.ns-anchat.orama-devnet.network." {
		t.Fatalf("first candidate is %q; the namespace wildcard must be tried before the base one", got[0])
	}
}
