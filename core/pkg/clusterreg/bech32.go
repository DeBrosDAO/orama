package clusterreg

import (
	"fmt"
	"strings"
)

const (
	accountHRP    = "orama"
	bech32Charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"
	accountBytes  = 20
)

// CanonicalAccount accepts an orama bech32 account and returns it unchanged
// when it is the canonical encoding of its 20 bytes. core does not import the
// chain module; this is the same check x/nodes CanonicalAddress performs.
func CanonicalAccount(addr string) (string, error) {
	if addr == "" || addr != strings.ToLower(addr) {
		return "", fmt.Errorf("address must be a lowercase orama bech32 account")
	}
	hrp, data, err := bech32Decode(addr)
	if err != nil {
		return "", err
	}
	if hrp != accountHRP {
		return "", fmt.Errorf("address prefix is not %s", accountHRP)
	}
	if len(data) != accountBytes {
		return "", fmt.Errorf("address is %d bytes", len(data))
	}
	enc, err := bech32Encode(accountHRP, data)
	if err != nil {
		return "", err
	}
	if enc != addr {
		return "", fmt.Errorf("address is not canonical")
	}
	return enc, nil
}

func bech32Decode(addr string) (string, []byte, error) {
	if strings.IndexFunc(addr, func(r rune) bool { return r < 33 || r > 126 }) >= 0 {
		return "", nil, fmt.Errorf("bech32: empty or non-printable")
	}
	pos := strings.LastIndexByte(addr, '1')
	if pos < 1 || pos+7 > len(addr) {
		return "", nil, fmt.Errorf("bech32: missing separator")
	}
	hrp, payload := addr[:pos], addr[pos+1:]
	values := make([]byte, len(payload))
	for i := 0; i < len(payload); i++ {
		v := strings.IndexByte(bech32Charset, payload[i])
		if v < 0 {
			return "", nil, fmt.Errorf("bech32: invalid character")
		}
		values[i] = byte(v)
	}
	if bech32Polymod(bech32HRPExpand(hrp), values) != 1 {
		return "", nil, fmt.Errorf("bech32: bad checksum")
	}
	data, err := bech32ConvertBits(values[:len(values)-6], 5, 8, false)
	if err != nil {
		return "", nil, err
	}
	return hrp, data, nil
}

func bech32Encode(hrp string, data []byte) (string, error) {
	five, err := bech32ConvertBits(data, 8, 5, true)
	if err != nil {
		return "", err
	}
	checksum := bech32Checksum(hrp, five)
	var b strings.Builder
	b.WriteString(hrp)
	b.WriteByte('1')
	for _, v := range append(five, checksum...) {
		b.WriteByte(bech32Charset[v])
	}
	return b.String(), nil
}

func bech32Checksum(hrp string, data []byte) []byte {
	values := append(append(bech32HRPExpand(hrp), data...), []byte{0, 0, 0, 0, 0, 0}...)
	mod := bech32Polymod(nil, values) ^ 1
	out := make([]byte, 6)
	for i := 0; i < 6; i++ {
		out[i] = byte((mod >> uint(5*(5-i))) & 31)
	}
	return out
}

func bech32HRPExpand(hrp string) []byte {
	out := make([]byte, 0, len(hrp)*2+1)
	for i := 0; i < len(hrp); i++ {
		out = append(out, hrp[i]>>5)
	}
	out = append(out, 0)
	for i := 0; i < len(hrp); i++ {
		out = append(out, hrp[i]&31)
	}
	return out
}

func bech32Polymod(hrp, values []byte) int {
	gen := []int{0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3}
	chk := 1
	eat := func(v byte) {
		b := (chk >> 25) & 0xff
		chk = ((chk & 0x1ffffff) << 5) ^ int(v)
		for i := 0; i < 5; i++ {
			if (b>>uint(i))&1 == 1 {
				chk ^= gen[i]
			}
		}
	}
	for _, v := range hrp {
		eat(v)
	}
	for _, v := range values {
		eat(v)
	}
	return chk
}

func bech32ConvertBits(data []byte, fromBits, toBits uint, pad bool) ([]byte, error) {
	acc := 0
	bits := uint(0)
	maxv := (1 << toBits) - 1
	maxAcc := (1 << (fromBits + toBits - 1)) - 1
	out := make([]byte, 0, len(data)*int(fromBits)/int(toBits)+1)
	for _, value := range data {
		if uint(value)>>fromBits != 0 {
			return nil, fmt.Errorf("bech32: value out of range")
		}
		acc = ((acc << fromBits) | int(value)) & maxAcc
		bits += fromBits
		for bits >= toBits {
			bits -= toBits
			out = append(out, byte((acc>>bits)&maxv))
		}
	}
	if pad {
		if bits > 0 {
			out = append(out, byte((acc<<(toBits-bits))&maxv))
		}
	} else if bits >= fromBits || ((acc<<(toBits-bits))&maxv) != 0 {
		return nil, fmt.Errorf("bech32: padding")
	}
	return out, nil
}
