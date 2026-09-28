package wasmpolicy

import (
	"fmt"

	"cosmossdk.io/math"
)

// Meter charges a state deposit of bytes * deposit_per_byte and refunds that amount on delete.
// It does not touch a bank keeper: the caller moves the coins.
type Meter struct {
	perByte math.Int
	open    map[string]math.Int
}

// NewMeter returns a meter that prices each stored byte at perByte.
// perByte must be non-nil and non-negative.
func NewMeter(perByte math.Int) (*Meter, error) {
	if perByte.IsNil() || perByte.IsNegative() {
		return nil, fmt.Errorf("deposit_per_byte must be a non-negative integer")
	}
	return &Meter{perByte: perByte, open: map[string]math.Int{}}, nil
}

// Charge records deposit = bytes * deposit_per_byte for id.
func (m *Meter) Charge(id string, bytes uint64) (math.Int, error) {
	if id == "" {
		return math.Int{}, fmt.Errorf("deposit id is empty")
	}
	if _, ok := m.open[id]; ok {
		return math.Int{}, fmt.Errorf("deposit id %q is already open", id)
	}
	deposit := math.NewIntFromUint64(bytes).Mul(m.perByte)
	m.open[id] = deposit
	return deposit, nil
}

// Refund returns the full deposit charged for id and forgets it.
func (m *Meter) Refund(id string) (math.Int, error) {
	deposit, ok := m.open[id]
	if !ok {
		return math.Int{}, fmt.Errorf("deposit id %q is not open", id)
	}
	delete(m.open, id)
	return deposit, nil
}

// StateDeposit is the thin wrapper wasmd does not provide. Contract storage calls it;
// wasmd itself is not forked.
type StateDeposit struct {
	meter *Meter
}

// NewStateDeposit returns a wrapper around a meter priced at perByte.
func NewStateDeposit(perByte math.Int) (StateDeposit, error) {
	meter, err := NewMeter(perByte)
	if err != nil {
		return StateDeposit{}, err
	}
	return StateDeposit{meter: meter}, nil
}

// Charge calls the meter for a write of nBytes.
func (s StateDeposit) Charge(id string, nBytes uint64) (math.Int, error) {
	return s.meter.Charge(id, nBytes)
}

// Refund calls the meter when the stored bytes are deleted.
func (s StateDeposit) Refund(id string) (math.Int, error) {
	return s.meter.Refund(id)
}
