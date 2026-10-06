package confidential

import (
	"errors"
	"testing"
)

func TestAcceptRefusesEveryReport(t *testing.T) {
	for _, report := range [][]byte{nil, {}, []byte("sev-snp"), []byte("tdx")} {
		err := Accept(report)
		if !errors.Is(err, ErrNoAttestation) {
			t.Fatalf("report %q: %v", report, err)
		}
	}
}
