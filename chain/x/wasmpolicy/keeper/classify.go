package keeper

import (
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
