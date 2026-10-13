// Package account makes the load generator's accounts and tracks their transaction sequences.
// A bench account is derived from a seed string and an index, so a re-run, a cache built earlier
// and the process that proved a bundle for one all agree on who an account is.
package account

import (
	"crypto/sha256"
	"fmt"

	"github.com/DeBrosOfficial/network/chain/client/tx"
)

// KeyDomain separates the bench keys from any other use of SHA-256 over a seed.
const KeyDomain = "oramad-bench/v1"

// Derive returns the index-th account of seed. The private key is SHA-256(domain | seed | index);
// these keys are for a throwaway local chain and are not secrets.
func Derive(seed string, index int) (tx.Account, error) {
	if seed == "" {
		return tx.Account{}, fmt.Errorf("account seed is empty")
	}
	if index < 0 {
		return tx.Account{}, fmt.Errorf("account index %d is negative", index)
	}
	key := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%d", KeyDomain, seed, index)))
	acct, err := tx.DeriveAccount(key[:])
	if err != nil {
		return tx.Account{}, fmt.Errorf("derive account %d of seed %q: %w", index, seed, err)
	}
	return acct, nil
}

// DeriveN returns accounts 0 to n-1 of seed.
func DeriveN(seed string, n int) ([]tx.Account, error) {
	if n <= 0 {
		return nil, fmt.Errorf("account count %d must be positive", n)
	}
	out := make([]tx.Account, n)
	for i := range out {
		acct, err := Derive(seed, i)
		if err != nil {
			return nil, err
		}
		out[i] = acct
	}
	return out, nil
}
