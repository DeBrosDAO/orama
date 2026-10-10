package app_test

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"
)

// The committed unshield vectors are bound to these two addresses. If a key derivation changes,
// the vectors must be regenerated (see examples/gen_flow_vectors.rs).
func TestShieldedTestKeys_matchTheAddressesTheVectorsAreBoundTo(t *testing.T) {
	require.Equal(t, "5f64e1128afde934c792939c8e64fca46b7f71f6", hex.EncodeToString(addr(secretKey("orama-shielded-test-alice"))))
	require.Equal(t, "01cf624b3ec99d3f174f9e431733f31659a3eee7", hex.EncodeToString(addr(secretKey("orama-shielded-test-committee-0"))))
}
