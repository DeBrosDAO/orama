package clusterreg

import (
	"encoding/hex"
	"fmt"
	"math/bits"
	"strconv"
	"strings"
)

const (
	// CreateDealTypeURL is the Any type URL of orama.storage.v1.MsgCreateDeal.
	CreateDealTypeURL = "/orama.storage.v1.MsgCreateDeal"
	// ExtendDealTypeURL is the Any type URL of orama.storage.v1.MsgExtendDeal.
	ExtendDealTypeURL = "/orama.storage.v1.MsgExtendDeal"
	// AcceptDealTypeURL is the Any type URL of orama.storage.v1.MsgAcceptDeal.
	AcceptDealTypeURL = "/orama.storage.v1.MsgAcceptDeal"
	// DeclineDealTypeURL is the Any type URL of orama.storage.v1.MsgDeclineDeal.
	DeclineDealTypeURL = "/orama.storage.v1.MsgDeclineDeal"

	// DealClassPrivate and DealClassPublicPin are the user deal classes.
	// ARCHIVE is not a user class.
	DealClassPrivate   = 1
	DealClassPublicPin = 2

	dealNonceLen         = 32
	dealRootLen          = 32
	dealLeafSize         = 1024
	minDealReplicas      = 3
	maxDealReplicas      = 32
	maxDealDurationEpoch = 1_000_000
	maxStorageIDLen      = 128
)

// Piece is one orama.storage.v1.PieceCommitment. Leaf counts are derived from
// the byte length with the 1024-byte piece rule. The root is not recomputed.
type Piece struct {
	Root            []byte
	RealLeafCount   uint64
	PaddedLeafCount uint64
	PieceBytes      uint64
}

// Deal is MsgCreateDeal. PricePerEpoch is a positive decimal norama amount.
type Deal struct {
	Signer         string
	Granter        string
	Class          int
	Nonce          []byte
	RepairDelegate string
	Replicas       uint32
	PricePerEpoch  string
	DurationEpochs uint64
	Pieces         []Piece
}

// Extend is MsgExtendDeal.
type Extend struct {
	Signer      string
	DealID      uint64
	ExtraEpochs uint64
}

// SlotAct is MsgAcceptDeal, or MsgDeclineDeal when Reason is set.
type SlotAct struct {
	Signer string
	NodeID string
	DealID uint64
	Slot   uint32
	Reason string
}

// ParseDealClass accepts private and public-pin. Archive is refused.
func ParseDealClass(s string) (int, error) {
	switch s {
	case "private":
		return DealClassPrivate, nil
	case "public-pin":
		return DealClassPublicPin, nil
	case "archive":
		return 0, fmt.Errorf("archive deals are created by the chain, not by a user")
	default:
		return 0, fmt.Errorf("class must be private or public-pin")
	}
}

// ParseNonce decodes a 32-byte hex deal nonce.
func ParseNonce(s string) ([]byte, error) {
	raw, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("nonce is not hex")
	}
	if len(raw) != dealNonceLen {
		return nil, fmt.Errorf("nonce must be %d bytes, got %d", dealNonceLen, len(raw))
	}
	return raw, nil
}

// ParsePieceSpec parses "<64 hex root>:<byte count>".
func ParsePieceSpec(spec string) (Piece, error) {
	rootHex, sizeText, ok := strings.Cut(spec, ":")
	if !ok || rootHex == "" || sizeText == "" {
		return Piece{}, fmt.Errorf("piece must be <64-hex-root>:<bytes>")
	}
	root, err := hex.DecodeString(rootHex)
	if err != nil {
		return Piece{}, fmt.Errorf("piece root is not hex")
	}
	n, err := strconv.ParseUint(sizeText, 10, 64)
	if err != nil || strings.HasPrefix(sizeText, "0") {
		return Piece{}, fmt.Errorf("piece byte count must be a positive integer")
	}
	return NewPiece(root, n)
}

// NewPiece fills the leaf counts the chain's CheckShape requires.
func NewPiece(root []byte, pieceBytes uint64) (Piece, error) {
	if len(root) != dealRootLen {
		return Piece{}, fmt.Errorf("piece root must be %d bytes, got %d", dealRootLen, len(root))
	}
	if pieceBytes == 0 {
		return Piece{}, fmt.Errorf("piece bytes must be positive")
	}
	real := (pieceBytes + dealLeafSize - 1) / dealLeafSize
	return Piece{
		Root:            append([]byte(nil), root...),
		RealLeafCount:   real,
		PaddedLeafCount: paddedLeafCount(real),
		PieceBytes:      pieceBytes,
	}, nil
}

func paddedLeafCount(real uint64) uint64 {
	if real == 0 || real&(real-1) == 0 {
		return real
	}
	return 1 << bits.Len64(real-1)
}

// ValidateDeal checks the stateless rules of MsgCreateDeal.
func ValidateDeal(d Deal) error {
	if _, err := CanonicalAccount(d.Signer); err != nil {
		return fmt.Errorf("signer: %w", err)
	}
	if d.Granter != "" {
		if _, err := CanonicalAccount(d.Granter); err != nil {
			return fmt.Errorf("granter: %w", err)
		}
	}
	if d.Class != DealClassPrivate && d.Class != DealClassPublicPin {
		return fmt.Errorf("class must be private or public-pin")
	}
	if len(d.Nonce) != dealNonceLen {
		return fmt.Errorf("nonce must be %d bytes, got %d", dealNonceLen, len(d.Nonce))
	}
	if err := storageID("repair delegate", d.RepairDelegate, true); err != nil {
		return err
	}
	if d.Replicas < minDealReplicas || d.Replicas > maxDealReplicas {
		return fmt.Errorf("replicas must be in [%d, %d]", minDealReplicas, maxDealReplicas)
	}
	if !positiveInteger(d.PricePerEpoch) {
		return fmt.Errorf("price per epoch must be a positive integer of norama")
	}
	if d.DurationEpochs == 0 || d.DurationEpochs > maxDealDurationEpoch {
		return fmt.Errorf("duration epochs must be in [1, %d]", maxDealDurationEpoch)
	}
	switch d.Class {
	case DealClassPrivate:
		if uint32(len(d.Pieces)) != d.Replicas {
			return fmt.Errorf("private deals need one piece per replica, got %d for %d replicas", len(d.Pieces), d.Replicas)
		}
	case DealClassPublicPin:
		if len(d.Pieces) != 1 {
			return fmt.Errorf("public-pin deals need exactly one piece, got %d", len(d.Pieces))
		}
	}
	for i := range d.Pieces {
		if err := checkPiece(d.Pieces[i]); err != nil {
			return fmt.Errorf("piece %d: %w", i, err)
		}
	}
	return nil
}

func checkPiece(p Piece) error {
	want, err := NewPiece(p.Root, p.PieceBytes)
	if err != nil {
		return err
	}
	if p.RealLeafCount != want.RealLeafCount || p.PaddedLeafCount != want.PaddedLeafCount {
		return fmt.Errorf("leaf counts do not match %d bytes", p.PieceBytes)
	}
	return nil
}

// ValidateExtend checks MsgExtendDeal.
func ValidateExtend(e Extend) error {
	if _, err := CanonicalAccount(e.Signer); err != nil {
		return fmt.Errorf("signer: %w", err)
	}
	if e.DealID == 0 {
		return fmt.Errorf("deal id is required")
	}
	if e.ExtraEpochs == 0 || e.ExtraEpochs > maxDealDurationEpoch {
		return fmt.Errorf("extra epochs must be in [1, %d]", maxDealDurationEpoch)
	}
	return nil
}

// ValidateSlot checks MsgAcceptDeal and MsgDeclineDeal.
func ValidateSlot(s SlotAct) error {
	if _, err := CanonicalAccount(s.Signer); err != nil {
		return fmt.Errorf("signer: %w", err)
	}
	if err := storageID("node id", s.NodeID, false); err != nil {
		return err
	}
	if s.DealID == 0 {
		return fmt.Errorf("deal id is required")
	}
	return nil
}

func storageID(name, id string, allowEmpty bool) error {
	if id == "" {
		if allowEmpty {
			return nil
		}
		return fmt.Errorf("%s is required", name)
	}
	if len(id) > maxStorageIDLen {
		return fmt.Errorf("%s is longer than %d bytes", name, maxStorageIDLen)
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.' || r == '_' || r == ':' || r == '-':
		default:
			return fmt.Errorf("%s contains %q", name, r)
		}
	}
	return nil
}

// EncodeDeal is the protobuf orama.storage.v1.MsgCreateDeal.
// The price field is always written, including a zero-length string, because
// the generated marshaler writes that custom Int field unconditionally.
func EncodeDeal(d Deal) []byte {
	out := appendStringField(nil, 1, d.Signer)
	if d.Granter != "" {
		out = appendStringField(out, 2, d.Granter)
	}
	if d.Class != 0 {
		out = appendUvarintField(out, 3, uint64(d.Class))
	}
	if len(d.Nonce) != 0 {
		out = appendBytesField(out, 4, d.Nonce)
	}
	if d.RepairDelegate != "" {
		out = appendStringField(out, 5, d.RepairDelegate)
	}
	if d.Replicas != 0 {
		out = appendUvarintField(out, 6, uint64(d.Replicas))
	}
	out = appendStringField(out, 7, d.PricePerEpoch)
	if d.DurationEpochs != 0 {
		out = appendUvarintField(out, 8, d.DurationEpochs)
	}
	for i := range d.Pieces {
		out = appendBytesField(out, 9, encodePiece(d.Pieces[i]))
	}
	return out
}

func encodePiece(p Piece) []byte {
	out := appendBytesField(nil, 1, p.Root)
	if p.RealLeafCount != 0 {
		out = appendUvarintField(out, 2, p.RealLeafCount)
	}
	if p.PaddedLeafCount != 0 {
		out = appendUvarintField(out, 3, p.PaddedLeafCount)
	}
	if p.PieceBytes != 0 {
		out = appendUvarintField(out, 4, p.PieceBytes)
	}
	return out
}

// EncodeExtend is the protobuf orama.storage.v1.MsgExtendDeal.
func EncodeExtend(e Extend) []byte {
	out := appendStringField(nil, 1, e.Signer)
	if e.DealID != 0 {
		out = appendUvarintField(out, 2, e.DealID)
	}
	if e.ExtraEpochs != 0 {
		out = appendUvarintField(out, 3, e.ExtraEpochs)
	}
	return out
}

// EncodeAccept is the protobuf orama.storage.v1.MsgAcceptDeal.
func EncodeAccept(s SlotAct) []byte {
	return encodeSlot(s, false)
}

// EncodeDecline is the protobuf orama.storage.v1.MsgDeclineDeal.
func EncodeDecline(s SlotAct) []byte {
	return encodeSlot(s, true)
}

func encodeSlot(s SlotAct, decline bool) []byte {
	out := appendStringField(nil, 1, s.Signer)
	if s.NodeID != "" {
		out = appendStringField(out, 2, s.NodeID)
	}
	if s.DealID != 0 {
		out = appendUvarintField(out, 3, s.DealID)
	}
	if s.Slot != 0 {
		out = appendUvarintField(out, 4, uint64(s.Slot))
	}
	if decline && s.Reason != "" {
		out = appendStringField(out, 5, s.Reason)
	}
	return out
}
