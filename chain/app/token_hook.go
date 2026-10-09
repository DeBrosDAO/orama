package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// errNoContractVM is what a build without libwasmvm answers to a token that names a transfer hook.
var errNoContractVM = errors.New("this build links no contract VM, so a token cannot have a transfer hook")

// contractTransferHook is x/token's transfer hook: a token that names a contract at creation has
// that contract called (sudo) on every MsgTransfer. x/token runs it under its gas cap. The wasm
// keeper does not exist when x/token's keeper is built, so installWasm binds it afterwards; a
// binary without the VM never binds it and refuses every hook.
type contractTransferHook struct {
	exists func(ctx context.Context, contract sdk.AccAddress) bool
	sudo   func(ctx context.Context, contract sdk.AccAddress, msg []byte) error
}

// transferHookMsg is the sudo message of a transfer hook: {"transfer_hook":{...}}.
type transferHookMsg struct {
	TransferHook transferHookArgs `json:"transfer_hook"`
}

type transferHookArgs struct {
	Denom  string `json:"denom"`
	From   string `json:"from"`
	To     string `json:"to"`
	Amount string `json:"amount"`
}

func (h *contractTransferHook) ValidateHook(ctx context.Context, contract sdk.AccAddress) error {
	if h.exists == nil {
		return errNoContractVM
	}
	if !h.exists(ctx, contract) {
		return fmt.Errorf("there is no contract at %s", contract)
	}
	return nil
}

func (h *contractTransferHook) OnTransfer(ctx context.Context, contract sdk.AccAddress, denom string, from, to sdk.AccAddress, amount math.Int) error {
	if h.sudo == nil {
		return errNoContractVM
	}
	msg, err := json.Marshal(transferHookMsg{TransferHook: transferHookArgs{Denom: denom, From: from.String(), To: to.String(), Amount: amount.String()}})
	if err != nil {
		return fmt.Errorf("failed to encode the transfer hook message: %w", err)
	}
	return h.sudo(ctx, contract, msg)
}
