package bundle

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/DeBrosOfficial/network/chain/x/shielded/verify"
)

func load(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "orchardffi", "testdata", name+".bundle"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParse_vectors(t *testing.T) {
	cases := []struct {
		name    string
		actions int
		vb      int64
	}{
		{"ironwood-1-action", 1, -5000},
		{"ironwood-2-action", 2, -5000},
		{"ironwood-transfer", 1, 100},
		{"ironwood-unshield", 1, 4900},
	}
	for _, c := range cases {
		b, err := Parse(load(t, c.name))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if b.Actions != c.actions || len(b.Nullifiers) != c.actions || len(b.Commitments) != c.actions {
			t.Errorf("%s: %d actions, %d nullifiers, %d commitments", c.name, b.Actions, len(b.Nullifiers), len(b.Commitments))
		}
		if b.ValueBalance != c.vb {
			t.Errorf("%s: value balance %d, want %d", c.name, b.ValueBalance, c.vb)
		}
		for i := range b.Nullifiers {
			if b.Nullifiers[i] == ([NodeLen]byte{}) || b.Commitments[i] == ([NodeLen]byte{}) {
				t.Errorf("%s: action %d has an empty nullifier or commitment", c.name, i)
			}
		}
	}
}

func TestParse_readsTheBytesTheCircuitCommitsTo(t *testing.T) {
	raw := load(t, "ironwood-1-action")
	b, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	// One action: count (1), then cv, nf, rk, cmx at 32-byte steps.
	if string(b.Nullifiers[0][:]) != string(raw[1+32:1+64]) || string(b.Commitments[0][:]) != string(raw[1+96:1+128]) {
		t.Fatal("nullifier or commitment read from the wrong offset")
	}
}

func TestParse_shieldingBundleAnchorIsTheEmptyRootTheFFIReports(t *testing.T) {
	b, err := Parse(load(t, "ironwood-1-action"))
	if err != nil {
		t.Fatal(err)
	}
	if b.Anchor == ([NodeLen]byte{}) {
		t.Fatal("anchor was not read")
	}
	two, _ := Parse(load(t, "ironwood-2-action"))
	if two.Anchor != b.Anchor {
		t.Fatal("the shielding vectors do not share the empty-tree anchor")
	}
}

func TestParse_refusesWhatTheVerifiersRefuse(t *testing.T) {
	good := load(t, "ironwood-1-action")
	proofLenAt := 1 + ActionLen + HeaderLen
	short := append([]byte(nil), good...)
	short[proofLenAt+1]--
	cases := map[string]struct {
		in   []byte
		want error
	}{
		"empty":               {nil, verify.ErrMalformed},
		"oversize":            {make([]byte, MaxBytes+1), verify.ErrMalformed},
		"zero actions":        {[]byte{0}, verify.ErrMalformed},
		"truncated header":    {good[:1+ActionLen], verify.ErrMalformed},
		"truncated body":      {good[:len(good)-1], verify.ErrMalformed},
		"trailing byte":       {append(append([]byte(nil), good...), 0), verify.ErrMalformed},
		"proof too short":     {short, verify.ErrProofLength},
		"non-canonical count": {append([]byte{0xfd, 1, 0}, good[1:]...), verify.ErrMalformed},
	}
	for name, c := range cases {
		if _, err := Parse(c.in); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", name, err, c.want)
		}
	}
}

func TestReadCompactSize(t *testing.T) {
	cases := []struct {
		in   []byte
		want uint64
		used int
		ok   bool
	}{
		{[]byte{0}, 0, 1, true},
		{[]byte{252}, 252, 1, true},
		{[]byte{253, 253, 0}, 253, 3, true},
		{[]byte{253, 252, 0}, 0, 3, false}, // not minimal
		{[]byte{253, 1}, 0, 0, false},      // short
		{[]byte{254, 0, 0, 1, 0}, 1 << 16, 5, true},
		{[]byte{254, 255, 255, 0, 0}, 0, 5, false},
		{[]byte{255, 0, 0, 0, 0, 1, 0, 0, 0}, 1 << 32, 9, true},
		{nil, 0, 0, false},
	}
	for _, c := range cases {
		v, used, ok := ReadCompactSize(c.in)
		if ok != c.ok || (ok && (v != c.want || used != c.used)) {
			t.Errorf("%v: got %d, %d, %v", c.in, v, used, ok)
		}
	}
}

func TestEffectingData_isThePrefixBeforeTheProof(t *testing.T) {
	raw := load(t, "ironwood-2-action")
	prefix, err := EffectingData(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(prefix) != 1+2*ActionLen+HeaderLen {
		t.Fatalf("prefix is %d bytes", len(prefix))
	}
}
