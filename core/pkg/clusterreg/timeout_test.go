package clusterreg

import "testing"

func TestTxHash_isTheSHA256OfTheBytesInUpperCaseHex(t *testing.T) {
	// SHA-256 of the empty input.
	if got := TxHash(nil); got != "E3B0C44298FC1C149AFBF4C8996FB92427AE41E4649B934CA495991B7852B855" {
		t.Fatalf("TxHash(nil) = %s", got)
	}
}

func TestTimeoutHeightAfter(t *testing.T) {
	if got := TimeoutHeightAfter(1000); got != 1000+TimeoutHeightMargin {
		t.Fatalf("got %d", got)
	}
}
