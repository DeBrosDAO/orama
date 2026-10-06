package auth

import (
	"encoding/hex"
	"strings"
)

// validateEVMWalletAddress reports whether a string is an EVM address: 40 hex
// characters, with or without the 0x prefix.
//
// It is deliberately not httputil.ValidateWalletAddress, which also accepts a
// Solana address. The one caller reads an address out of RootWallet, which
// holds an EVM key, so a base58 string there is a sign something went wrong
// rather than a wallet to carry on with.
func validateEVMWalletAddress(address string) bool {
	addr := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(address)), "0x")
	if len(addr) != 40 {
		return false
	}
	_, err := hex.DecodeString(addr)
	return err == nil
}
