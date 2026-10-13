package types

import (
	"encoding/json"
	"fmt"

	collcodec "cosmossdk.io/collections/codec"
)

// JSONValue is a collections value codec that stores T as encoding/json. wasmpolicy's state is
// not proto: its genesis is already encoding/json, and its rows are small.
type JSONValue[T any] struct{}

var _ collcodec.ValueCodec[DepositChunk] = JSONValue[DepositChunk]{}

// Encode implements collcodec.ValueCodec.
func (JSONValue[T]) Encode(value T) ([]byte, error) { return json.Marshal(value) }

// Decode implements collcodec.ValueCodec.
func (JSONValue[T]) Decode(b []byte) (T, error) {
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return v, err
	}
	return v, nil
}

// EncodeJSON implements collcodec.ValueCodec.
func (c JSONValue[T]) EncodeJSON(value T) ([]byte, error) { return c.Encode(value) }

// DecodeJSON implements collcodec.ValueCodec.
func (c JSONValue[T]) DecodeJSON(b []byte) (T, error) { return c.Decode(b) }

// Stringify implements collcodec.ValueCodec.
func (JSONValue[T]) Stringify(value T) string { return fmt.Sprintf("%+v", value) }

// ValueType implements collcodec.ValueCodec.
func (JSONValue[T]) ValueType() string { return fmt.Sprintf("json/%T", *new(T)) }
