//go:build e2e_fleet

package chain

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

// AccountPrefix is the bech32 prefix of account addresses (chain/app/params).
const AccountPrefix = "orama"

// UnreachableAuthorityName is the never-registered module name whose address
// every authority-gated module is given (chain/app/app.go
// UnreachableAuthority, docs/CHAIN.md "Modules wired").
const UnreachableAuthorityName = "orama/no-authority"

const bech32Charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"

// ModuleAddress is authtypes.NewModuleAddress(name) as a bech32 account
// address: the first 20 bytes of sha256(name).
func ModuleAddress(name string) string {
	sum := sha256.Sum256([]byte(name))
	addr, err := Bech32(AccountPrefix, sum[:20])
	if err != nil {
		panic(fmt.Sprintf("bech32 of a 20-byte hash cannot fail: %v", err))
	}
	return addr
}

// Bech32 encodes data (8-bit bytes) under hrp (BIP-173).
func Bech32(hrp string, data []byte) (string, error) {
	five, err := convertBits(data, 8, 5, true)
	if err != nil {
		return "", err
	}
	check := bech32Checksum(hrp, five)
	var b strings.Builder
	b.WriteString(hrp)
	b.WriteByte('1')
	for _, v := range append(five, check...) {
		b.WriteByte(bech32Charset[v])
	}
	return b.String(), nil
}

// Bech32Decode returns the 8-bit payload of a bech32 string with prefix hrp
// (checksum verified): an account address's 20 bytes.
func Bech32Decode(hrp, s string) ([]byte, error) {
	s = strings.ToLower(s)
	sep := strings.LastIndexByte(s, '1')
	if sep < 1 || s[:sep] != hrp || len(s)-sep-1 < 6 {
		return nil, fmt.Errorf("%q is not a bech32 %s string", s, hrp)
	}
	data := make([]byte, 0, len(s)-sep-1)
	for _, ch := range s[sep+1:] {
		v := strings.IndexRune(bech32Charset, ch)
		if v < 0 {
			return nil, fmt.Errorf("%q has a character outside the bech32 set", s)
		}
		data = append(data, byte(v))
	}
	if !equalBytes(bech32Checksum(hrp, data[:len(data)-6]), data[len(data)-6:]) {
		return nil, fmt.Errorf("%q has a bad bech32 checksum", s)
	}
	return convertBits(data[:len(data)-6], 5, 8, false)
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func bech32Polymod(values []byte) uint32 {
	gen := [5]uint32{0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3}
	chk := uint32(1)
	for _, v := range values {
		top := chk >> 25
		chk = (chk&0x1ffffff)<<5 ^ uint32(v)
		for i := 0; i < 5; i++ {
			if (top>>uint(i))&1 == 1 {
				chk ^= gen[i]
			}
		}
	}
	return chk
}

func bech32Checksum(hrp string, data []byte) []byte {
	values := make([]byte, 0, len(hrp)*2+1+len(data)+6)
	for i := 0; i < len(hrp); i++ {
		values = append(values, hrp[i]>>5)
	}
	values = append(values, 0)
	for i := 0; i < len(hrp); i++ {
		values = append(values, hrp[i]&31)
	}
	values = append(append(values, data...), 0, 0, 0, 0, 0, 0)
	mod := bech32Polymod(values) ^ 1
	out := make([]byte, 6)
	for i := range out {
		out[i] = byte((mod >> uint(5*(5-i))) & 31)
	}
	return out
}

func convertBits(data []byte, from, to uint, pad bool) ([]byte, error) {
	var acc, bits uint
	maxv := uint(1)<<to - 1
	out := make([]byte, 0, len(data)*int(from)/int(to)+1)
	for _, v := range data {
		acc = acc<<from | uint(v)
		bits += from
		for bits >= to {
			bits -= to
			out = append(out, byte((acc>>bits)&maxv))
		}
	}
	if pad && bits > 0 {
		out = append(out, byte((acc<<(to-bits))&maxv))
	} else if !pad && (bits >= from || (acc<<(to-bits))&maxv != 0) {
		return nil, fmt.Errorf("invalid padding")
	}
	return out, nil
}
