package inclusion

import (
	"bytes"
	"crypto/ed25519"
	"encoding/binary"
	"fmt"
	"math"
	"sort"
)

const (
	signDomain  = "orama-inclusion-vote-extension-v1"
	wireMagic   = "ORIN"
	wireVersion = 1
)

// Extension is one validator's vote extension.
// PubKey and Signature are verified with ed25519. Power is not signed;
// the caller sets it from the validator set before PrepareCommit or Process.
type Extension struct {
	PubKey    ed25519.PublicKey
	Power     int64
	Height    int64
	Round     int32
	Txs       [][]byte
	Signature []byte
}

// BuildExtension packs candidate transactions into a signed vote extension.
// Transactions that do not decode, pay less than v.BaseFee, or do not fit
// under ListMaxBytes and MaxSenderBytes are left out. Packing walks the
// candidates in lexicographic order and skips any that do not fit, so a
// large transaction does not crowd out a later smaller one. An empty list
// is signed when nothing fits; an empty list still counts toward quorum.
func BuildExtension(priv ed25519.PrivateKey, power int64, v View, candidates [][]byte) (Extension, error) {
	if err := v.Params.Validate(); err != nil {
		return Extension{}, err
	}
	if len(priv) != ed25519.PrivateKeySize {
		return Extension{}, fmt.Errorf("inclusion: ed25519 private key must be %d bytes", ed25519.PrivateKeySize)
	}
	if power <= 0 {
		return Extension{}, fmt.Errorf("inclusion: voting power must be positive")
	}
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok || len(pub) != ed25519.PublicKeySize {
		return Extension{}, fmt.Errorf("inclusion: private key has no ed25519 public key")
	}

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
		meta, err := DecodeTx(tx)
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
		chosen = append(chosen, bytes.Clone(tx))
		used += len(tx)
		senderUsed[key] = have + len(tx)
	}

	if len(chosen) > math.MaxUint32 {
		return Extension{}, fmt.Errorf("inclusion: too many transactions")
	}
	ext := Extension{
		PubKey: bytes.Clone(pub),
		Power:  power,
		Height: v.Height,
		Round:  v.Round,
		Txs:    chosen,
	}
	ext.Signature = ed25519.Sign(priv, signingBytes(ext))
	return ext, nil
}

// MarshalBinary encodes the extension for a CometBFT vote extension.
// Power is omitted; it comes from the validator set, not the wire.
func (e Extension) MarshalBinary() ([]byte, error) {
	if len(e.PubKey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("inclusion: public key must be %d bytes", ed25519.PublicKeySize)
	}
	if len(e.Signature) != ed25519.SignatureSize {
		return nil, fmt.Errorf("inclusion: signature must be %d bytes", ed25519.SignatureSize)
	}
	if len(e.Txs) > math.MaxUint32 {
		return nil, fmt.Errorf("inclusion: too many transactions")
	}
	n := 4 + 1 + 8 + 4 + ed25519.PublicKeySize + ed25519.SignatureSize + 4
	for _, tx := range e.Txs {
		if len(tx) > math.MaxUint32 {
			return nil, fmt.Errorf("inclusion: transaction does not fit in the extension encoding")
		}
		n += 4 + len(tx)
	}
	out := make([]byte, 0, n)
	out = append(out, wireMagic...)
	out = append(out, wireVersion)
	var num [8]byte
	binary.BigEndian.PutUint64(num[:], uint64(e.Height))
	out = append(out, num[:]...)
	binary.BigEndian.PutUint32(num[:4], uint32(e.Round))
	out = append(out, num[:4]...)
	out = append(out, e.PubKey...)
	out = append(out, e.Signature...)
	binary.BigEndian.PutUint32(num[:4], uint32(len(e.Txs)))
	out = append(out, num[:4]...)
	for _, tx := range e.Txs {
		binary.BigEndian.PutUint32(num[:4], uint32(len(tx)))
		out = append(out, num[:4]...)
		out = append(out, tx...)
	}
	return out, nil
}

// UnmarshalExtension decodes a vote extension produced by MarshalBinary.
// Power is left at zero. Trailing bytes are rejected.
func UnmarshalExtension(raw []byte) (Extension, error) {
	if len(raw) < 4+1+8+4+ed25519.PublicKeySize+ed25519.SignatureSize+4 {
		return Extension{}, fmt.Errorf("inclusion: extension encoding too short")
	}
	if string(raw[:4]) != wireMagic {
		return Extension{}, fmt.Errorf("inclusion: bad extension magic")
	}
	if raw[4] != wireVersion {
		return Extension{}, fmt.Errorf("inclusion: unsupported extension version %d", raw[4])
	}
	off := 5
	height := int64(binary.BigEndian.Uint64(raw[off:]))
	off += 8
	round := int32(binary.BigEndian.Uint32(raw[off:]))
	off += 4
	pub := bytes.Clone(raw[off : off+ed25519.PublicKeySize])
	off += ed25519.PublicKeySize
	sig := bytes.Clone(raw[off : off+ed25519.SignatureSize])
	off += ed25519.SignatureSize
	n := binary.BigEndian.Uint32(raw[off:])
	off += 4
	rest := len(raw) - off
	if int(n) > rest/4 {
		return Extension{}, fmt.Errorf("inclusion: extension transaction count exceeds the payload")
	}
	txs := make([][]byte, 0, n)
	for i := uint32(0); i < n; i++ {
		if len(raw)-off < 4 {
			return Extension{}, fmt.Errorf("inclusion: extension encoding truncated")
		}
		ln := binary.BigEndian.Uint32(raw[off:])
		off += 4
		if uint32(len(raw)-off) < ln {
			return Extension{}, fmt.Errorf("inclusion: extension transaction truncated")
		}
		txs = append(txs, bytes.Clone(raw[off:off+int(ln)]))
		off += int(ln)
	}
	if off != len(raw) {
		return Extension{}, fmt.Errorf("inclusion: extension encoding has trailing bytes")
	}
	return Extension{
		PubKey:    pub,
		Height:    height,
		Round:     round,
		Txs:       txs,
		Signature: sig,
	}, nil
}

func signingBytes(e Extension) []byte {
	var buf bytes.Buffer
	buf.WriteString(signDomain)
	var num [8]byte
	binary.BigEndian.PutUint64(num[:], uint64(e.Height))
	buf.Write(num[:])
	binary.BigEndian.PutUint32(num[:4], uint32(e.Round))
	buf.Write(num[:4])
	binary.BigEndian.PutUint32(num[:4], uint32(len(e.Txs)))
	buf.Write(num[:4])
	for _, tx := range e.Txs {
		binary.BigEndian.PutUint32(num[:4], uint32(len(tx)))
		buf.Write(num[:4])
		buf.Write(tx)
	}
	return buf.Bytes()
}

func (e Extension) validSig() bool {
	if len(e.PubKey) != ed25519.PublicKeySize || len(e.Signature) != ed25519.SignatureSize {
		return false
	}
	if len(e.Txs) > math.MaxUint32 {
		return false
	}
	for _, tx := range e.Txs {
		if len(tx) > math.MaxUint32 {
			return false
		}
	}
	return ed25519.Verify(e.PubKey, signingBytes(e), e.Signature)
}

func copyExtension(e Extension) Extension {
	out := Extension{
		PubKey:    bytes.Clone(e.PubKey),
		Power:     e.Power,
		Height:    e.Height,
		Round:     e.Round,
		Signature: bytes.Clone(e.Signature),
		Txs:       make([][]byte, len(e.Txs)),
	}
	for i, tx := range e.Txs {
		out.Txs[i] = bytes.Clone(tx)
	}
	return out
}
