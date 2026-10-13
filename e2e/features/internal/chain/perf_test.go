//go:build e2e_fleet

package chain

import (
	"strings"
	"testing"
	"time"
)

func TestParseSamples_okAndFailedLines(t *testing.T) {
	out := "noise\nS 2000000000 1500000 -\nS 1000000000 2500000 CheckTx_refused_it:_sequence_mismatch\n"
	got, err := parseSamples(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d samples, want 2", len(got))
	}
	if !got[0].At.Equal(time.Unix(1, 0)) || got[0].Duration != 2500*time.Microsecond || got[0].Err == nil ||
		!strings.Contains(got[0].Err.Error(), "CheckTx refused it: sequence mismatch") {
		t.Errorf("first (the earlier start) = %+v", got[0])
	}
	if got[1].Err != nil || got[1].Duration != 1500*time.Microsecond {
		t.Errorf("second = %+v", got[1])
	}
}

func TestParseSamples_emptyAndMalformed(t *testing.T) {
	for name, in := range map[string]string{
		"empty":             "",
		"only noise":        "python3: command not found\n",
		"missing fields":    "S 1 2\n",
		"negative duration": "S 1 -2 -\n",
		"not numbers":       "S a b -\n",
	} {
		if _, err := parseSamples(in); err == nil {
			t.Errorf("%s: parseSamples accepted %q", name, in)
		}
	}
}
