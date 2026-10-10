package inclusion

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"sort"
)

const (
	listMagic   = "ORIL"
	listVersion = 1
	listHeader  = len(listMagic) + 1 + 4
)

// SelectList picks the transactions a validator lists, from candidates it has
// seen. Candidates that do not decode, pay less than v.BaseFee, fail v.Admit,
// or do not fit under ListMaxBytes and MaxSenderBytes are left out. It walks
// the candidates in lexicographic order and skips any that do not fit, so a
// large transaction does not crowd out a later smaller one. The result is
// sorted and free of duplicates, the form ValidateList accepts. v.Params
// must already be valid.
func SelectList(v View, candidates [][]byte) [][]byte {
	ordered := append([][]byte(nil), candidates...)
	sort.Slice(ordered, func(i, j int) bool {
		return bytes.Compare(ordered[i], ordered[j]) < 0
	})

	chosen := make([][]byte, 0, len(ordered))
	used := 0
	senderUsed := make(map[string]int)
	var prev []byte
	for _, tx := range ordered {
		if len(tx) == 0 || (prev != nil && bytes.Equal(prev, tx)) {
			prev = tx
			continue
		}
		prev = tx
		meta, err := v.decode(tx)
		if err != nil || meta.Fee < v.BaseFee {
			continue
		}
		if len(tx) > math.MaxUint32 || len(tx) > v.Params.ListMaxBytes || len(tx) > v.Params.MaxSenderBytes {
			continue
		}
		if used > v.Params.ListMaxBytes-len(tx) {
			continue
		}
		key := SenderKey(meta.Sender)
		have := senderUsed[key]
		if have > v.Params.MaxSenderBytes-len(tx) {
			continue
		}
		if v.Admit != nil && !v.Admit(tx, meta) {
			continue
		}
		chosen = append(chosen, bytes.Clone(tx))
		used += len(tx)
		senderUsed[key] = have + len(tx)
	}
	return chosen
}

// EncodeList encodes a transaction list as the payload of a vote extension
// whose authenticity CometBFT already carries (View.Authenticated). An empty
// list encodes to a short header, not to nothing.
func EncodeList(txs [][]byte) ([]byte, error) {
	if len(txs) > math.MaxUint32 {
		return nil, fmt.Errorf("inclusion: too many transactions")
	}
	n := listHeader
	for _, tx := range txs {
		if len(tx) > math.MaxUint32 {
			return nil, fmt.Errorf("inclusion: transaction does not fit in the list encoding")
		}
		n += 4 + len(tx)
	}
	out := make([]byte, 0, n)
	out = append(out, listMagic...)
	out = append(out, listVersion)
	var num [4]byte
	binary.BigEndian.PutUint32(num[:], uint32(len(txs)))
	out = append(out, num[:]...)
	for _, tx := range txs {
		binary.BigEndian.PutUint32(num[:], uint32(len(tx)))
		out = append(out, num[:]...)
		out = append(out, tx...)
	}
	return out, nil
}

// DecodeList decodes an EncodeList payload. An empty payload is an empty
// list: a validator whose ExtendVote had nothing to say sends no bytes.
// Trailing bytes are rejected.
func DecodeList(raw []byte) ([][]byte, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if len(raw) < listHeader {
		return nil, fmt.Errorf("inclusion: list encoding too short")
	}
	if string(raw[:len(listMagic)]) != listMagic {
		return nil, fmt.Errorf("inclusion: bad list magic")
	}
	if raw[len(listMagic)] != listVersion {
		return nil, fmt.Errorf("inclusion: unsupported list version %d", raw[len(listMagic)])
	}
	off := len(listMagic) + 1
	n := binary.BigEndian.Uint32(raw[off:])
	off += 4
	if uint64(n) > uint64((len(raw)-off)/4) {
		return nil, fmt.Errorf("inclusion: list transaction count exceeds the payload")
	}
	txs := make([][]byte, 0, n)
	for i := uint32(0); i < n; i++ {
		if len(raw)-off < 4 {
			return nil, fmt.Errorf("inclusion: list encoding truncated")
		}
		ln := binary.BigEndian.Uint32(raw[off:])
		off += 4
		if uint64(len(raw)-off) < uint64(ln) {
			return nil, fmt.Errorf("inclusion: list transaction truncated")
		}
		txs = append(txs, bytes.Clone(raw[off:off+int(ln)]))
		off += int(ln)
	}
	if off != len(raw) {
		return nil, fmt.Errorf("inclusion: list encoding has trailing bytes")
	}
	return txs, nil
}

// ValidateList checks a decoded list against the rules that need no state:
// every transaction is non-empty, decodes, pays at least v.BaseFee and fits
// MaxSenderBytes, the transactions are strictly increasing (so sorted and
// unique), the sender caps hold, and the total is at most ListMaxBytes. It is
// deterministic, so every validator reaches the same verdict on the same list.
func ValidateList(v View, txs [][]byte) error {
	if err := v.Params.Validate(); err != nil {
		return err
	}
	total := 0
	senderUsed := make(map[string]int)
	for i, tx := range txs {
		if len(tx) == 0 {
			return fmt.Errorf("inclusion: list transaction %d is empty", i)
		}
		if i > 0 && bytes.Compare(txs[i-1], tx) >= 0 {
			return fmt.Errorf("inclusion: list transaction %d is not in strictly increasing order", i)
		}
		if len(tx) > v.Params.MaxSenderBytes || total > v.Params.ListMaxBytes-len(tx) {
			return fmt.Errorf("inclusion: list exceeds %d bytes", v.Params.ListMaxBytes)
		}
		total += len(tx)
		meta, err := v.decode(tx)
		if err != nil {
			return fmt.Errorf("inclusion: list transaction %d: %w", i, err)
		}
		if meta.Fee < v.BaseFee {
			return fmt.Errorf("inclusion: list transaction %d pays %d, under the base fee %d", i, meta.Fee, v.BaseFee)
		}
		key := SenderKey(meta.Sender)
		if senderUsed[key] > v.Params.MaxSenderBytes-len(tx) {
			return fmt.Errorf("inclusion: list transaction %d exceeds the per-sender cap", i)
		}
		senderUsed[key] += len(tx)
	}
	return nil
}
