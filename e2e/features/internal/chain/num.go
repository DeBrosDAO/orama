//go:build e2e_fleet

package chain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
)

// Int is a cosmossdk.io/math.Int or a (u)int64 as proto-JSON writes it: a
// quoted decimal string (or a bare number from older encoders). The zero
// value is 0.
type Int struct{ big.Int }

// UnmarshalJSON accepts "123", 123 and null.
func (i *Int) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if bytes.Equal(b, []byte("null")) {
		i.SetInt64(0)
		return nil
	}
	s := string(b)
	if len(b) > 0 && b[0] == '"' {
		if err := json.Unmarshal(b, &s); err != nil {
			return fmt.Errorf("integer %s: %w", b, err)
		}
	}
	if s == "" {
		i.SetInt64(0)
		return nil
	}
	if _, ok := i.SetString(s, 10); !ok {
		return fmt.Errorf("integer %q is not a base-10 integer", s)
	}
	return nil
}

// MarshalJSON writes the proto-JSON form (a quoted decimal string).
func (i Int) MarshalJSON() ([]byte, error) { return json.Marshal(i.String()) }

// NewInt returns an Int of v.
func NewInt(v int64) Int {
	var i Int
	i.SetInt64(v)
	return i
}

// Orama returns n whole ORAMA in norama.
func Orama(n int64) Int {
	var i Int
	i.Mul(big.NewInt(n), big.NewInt(NoramaPerOrama))
	return i
}

// Add returns i+o.
func (i Int) Add(o Int) Int {
	var r Int
	r.Int.Add(&i.Int, &o.Int)
	return r
}

// Sub returns i-o.
func (i Int) Sub(o Int) Int {
	var r Int
	r.Int.Sub(&i.Int, &o.Int)
	return r
}

// Cmp compares i and o like big.Int.Cmp.
func (i Int) Cmp(o Int) int { return i.Int.Cmp(&o.Int) }

// String is the decimal form.
func (i Int) String() string { return i.Int.String() }

// IsZero reports i == 0.
func (i Int) IsZero() bool { return i.Int.Sign() == 0 }

// Int64 is the value as int64, for heights and epochs (never for amounts).
func (i Int) Int64() int64 { return i.Int.Int64() }

// Dec is a cosmossdk.io/math.LegacyDec in proto-JSON ("0.500000000000000000").
type Dec struct{ big.Rat }

// UnmarshalJSON accepts a quoted decimal string.
func (d *Dec) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("decimal %s: %w", b, err)
	}
	if s == "" {
		d.SetInt64(0)
		return nil
	}
	if _, ok := d.SetString(s); !ok {
		return fmt.Errorf("decimal %q does not parse", s)
	}
	return nil
}

// Float is the decimal as a float64, for bounds checks only.
func (d Dec) Float() float64 {
	f, _ := d.Rat.Float64()
	return f
}

// Uint parses a decimal uint64 string, as the CLI prints heights.
func Uint(s string) (uint64, error) {
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not an unsigned integer: %w", s, err)
	}
	return v, nil
}
