package storageclient

import (
	"errors"
	"fmt"

	"google.golang.org/protobuf/encoding/protowire"
)

// Field numbers of the chain protos this package decodes
// (chain/proto/orama/storage/v1/storage.proto and nodes/v1/nodes.proto).
const (
	respBodyField = 1

	dealIDField       = 1
	dealNonceField    = 7
	dealReplicasField = 8

	slotDealField     = 1
	slotIndexField    = 2
	slotNodeField     = 3
	slotRootField     = 7
	slotStatusField   = 11
	slotAcceptedField = 13

	nodeEndpointsField = 6
)

func encodeUintField(num protowire.Number, v uint64) []byte {
	if v == 0 {
		return nil
	}
	b := protowire.AppendTag(nil, num, protowire.VarintType)
	return protowire.AppendVarint(b, v)
}

func encodeStringField(num protowire.Number, v string) []byte {
	b := protowire.AppendTag(nil, num, protowire.BytesType)
	return protowire.AppendString(b, v)
}

// eachField calls fn for every field of one message. fn receives the varint
// value for varint fields and the bytes for length-delimited ones.
func eachField(msg []byte, fn func(num protowire.Number, typ protowire.Type, v uint64, b []byte) error) error {
	for len(msg) > 0 {
		num, typ, n := protowire.ConsumeTag(msg)
		if n < 0 {
			return errors.New("bad protobuf tag")
		}
		msg = msg[n:]
		var v uint64
		var b []byte
		switch typ {
		case protowire.VarintType:
			v, n = protowire.ConsumeVarint(msg)
		case protowire.BytesType:
			b, n = protowire.ConsumeBytes(msg)
		default:
			n = protowire.ConsumeFieldValue(num, typ, msg)
		}
		if n < 0 {
			return fmt.Errorf("bad protobuf field %d", num)
		}
		msg = msg[n:]
		if err := fn(num, typ, v, b); err != nil {
			return err
		}
	}
	return nil
}

// unwrap returns field 1 of a Query*Response, the record itself.
func unwrap(resp []byte) ([]byte, error) {
	var body []byte
	err := eachField(resp, func(num protowire.Number, typ protowire.Type, _ uint64, b []byte) error {
		if num == respBodyField && typ == protowire.BytesType {
			body = b
		}
		return nil
	})
	return body, err
}

func decodeDeal(resp []byte) (Deal, error) {
	body, err := unwrap(resp)
	if err != nil {
		return Deal{}, fmt.Errorf("decode deal: %w", err)
	}
	var d Deal
	err = eachField(body, func(num protowire.Number, _ protowire.Type, v uint64, b []byte) error {
		switch num {
		case dealIDField:
			d.ID = v
		case dealNonceField:
			d.Nonce = append([]byte(nil), b...)
		case dealReplicasField:
			d.Replicas = uint32(v)
		}
		return nil
	})
	if err != nil {
		return Deal{}, fmt.Errorf("decode deal: %w", err)
	}
	if d.ID == 0 {
		return Deal{}, errors.New("decode deal: no deal id")
	}
	return d, nil
}

func decodeSlot(resp []byte) (Slot, error) {
	body, err := unwrap(resp)
	if err != nil {
		return Slot{}, fmt.Errorf("decode slot: %w", err)
	}
	var s Slot
	err = eachField(body, func(num protowire.Number, _ protowire.Type, v uint64, b []byte) error {
		switch num {
		case slotDealField:
			s.DealID = v
		case slotIndexField:
			s.Index = uint32(v)
		case slotNodeField:
			s.NodeID = string(b)
		case slotRootField:
			s.PieceRoot = append([]byte(nil), b...)
		case slotStatusField:
			s.Status = int32(v)
		case slotAcceptedField:
			s.Accepted = v != 0
		}
		return nil
	})
	if err != nil {
		return Slot{}, fmt.Errorf("decode slot: %w", err)
	}
	return s, nil
}

func decodeNodeEndpoints(resp []byte) ([]string, error) {
	body, err := unwrap(resp)
	if err != nil {
		return nil, fmt.Errorf("decode node: %w", err)
	}
	var endpoints []string
	err = eachField(body, func(num protowire.Number, typ protowire.Type, _ uint64, b []byte) error {
		if num == nodeEndpointsField && typ == protowire.BytesType {
			endpoints = append(endpoints, string(b))
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("decode node: %w", err)
	}
	return endpoints, nil
}
