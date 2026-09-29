package keeper

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/wasmpolicy/types"
)

const (
	wasmdStoreCode           = "cosmwasm.wasm.v1.MsgStoreCode"
	wasmdStoreAndInstantiate = "cosmwasm.wasm.v1.MsgStoreAndInstantiateContract"
)

// StoreCodeID is implemented by a store-code message that already names its code id.
// A fresh MsgStoreCode has no id; callers treat that as zero, which is not in the genesis set.
type StoreCodeID interface {
	GetStoreCodeID() (uint64, bool)
}

// SunsetChanger is implemented by any message that proposes a new upload_sunset_height.
// No such message is registered. CheckMsg rejects every one it sees and does not write.
type SunsetChanger interface {
	ProposedUploadSunset() (uint64, bool)
}

type namedMessage interface {
	XXX_MessageName() string
}

// AllowStore decides a MsgStoreCode at height.
// Before sunset, the store is rejected unless codeID is in the genesis code set.
// At sunset, and after it, the store is allowed. Nothing in this function writes the height.
func AllowStore(height int64, sunset uint64, codeID uint64, genesis map[uint64]struct{}) error {
	if height < 0 || uint64(height) < sunset {
		if _, ok := genesis[codeID]; ok {
			return nil
		}
		return types.ErrUploadClosed
	}
	return nil
}

func classifyMsg(msg sdk.Msg) (store bool, codeID uint64, changesSunset bool) {
	if msg == nil {
		return false, 0, false
	}
	if changer, ok := msg.(SunsetChanger); ok {
		if _, change := changer.ProposedUploadSunset(); change {
			return false, 0, true
		}
	}
	if coded, ok := msg.(StoreCodeID); ok {
		if id, isStore := coded.GetStoreCodeID(); isStore {
			return true, id, false
		}
	}
	if named, ok := msg.(namedMessage); ok {
		switch named.XXX_MessageName() {
		case wasmdStoreCode, wasmdStoreAndInstantiate:
			return true, 0, false
		}
	}
	return false, 0, false
}

// WasmByteCode is implemented by a store-code message (wasmd's MsgStoreCode and
// MsgStoreAndInstantiateContract) that carries the code bytes.
type WasmByteCode interface {
	GetWASMByteCode() []byte
}

// maxHashedWasmBytes bounds the uncompressed size codeHash will read. Anything
// larger is not hashed, so it cannot match the allow-list. It is far above
// wasmd's own max_wasm_code_size (800 KiB by default).
const maxHashedWasmBytes = 4 << 20

// gzipMagic starts a gzip stream. wasmd accepts an upload as raw wasm or gzip.
var gzipMagic = []byte{0x1f, 0x8b, 0x08}

// codeHash returns the lowercase hex SHA-256 of the uncompressed wasm in msg,
// which is the code hash x/houses lists. ok is false when msg carries no code,
// the gzip stream is invalid, or the code is over maxHashedWasmBytes.
func codeHash(msg sdk.Msg) (string, bool) {
	carrier, isCode := msg.(WasmByteCode)
	if !isCode {
		return "", false
	}
	code := carrier.GetWASMByteCode()
	if bytes.HasPrefix(code, gzipMagic) {
		zr, err := gzip.NewReader(bytes.NewReader(code))
		if err != nil {
			return "", false
		}
		defer zr.Close()
		code, err = io.ReadAll(io.LimitReader(zr, maxHashedWasmBytes+1))
		if err != nil {
			return "", false
		}
	}
	if len(code) == 0 || len(code) > maxHashedWasmBytes {
		return "", false
	}
	sum := sha256.Sum256(code)
	return hex.EncodeToString(sum[:]), true
}
