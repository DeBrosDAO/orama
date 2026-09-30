package wasmbindings

import (
	"context"
	"encoding/json"
	"fmt"

	wasmvmtypes "github.com/CosmWasm/wasmvm/v3/types"

	errorsmod "cosmossdk.io/errors"

	"github.com/cosmos/cosmos-sdk/baseapp"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
)

// Dispatcher is wasmd's Messenger: the handler the wasm keeper would use without this package.
type Dispatcher interface {
	DispatchMsg(ctx sdk.Context, contract sdk.AccAddress, portID string, msg wasmvmtypes.CosmosMsg) ([]sdk.Event, [][]byte, [][]*codectypes.Any, error)
}

// Router finds the message server for a message, as baseapp's MsgServiceRouter does.
type Router interface {
	Handler(msg sdk.Msg) baseapp.MsgServiceHandler
}

// EarningsPayer pays norama from a contract into a user's earnings account (x/fees).
type EarningsPayer interface {
	PayEarnings(ctx context.Context, payer, recipient sdk.AccAddress, amt sdk.Coin) error
}

// Messenger enforces the contract message policy and executes Orama custom messages. Everything
// it does not own goes to next.
type Messenger struct {
	next     Dispatcher
	router   Router
	earnings EarningsPayer
}

var _ Dispatcher = Messenger{}

// NewMessenger wraps next.
func NewMessenger(next Dispatcher, router Router, earnings EarningsPayer) Messenger {
	return Messenger{next: next, router: router, earnings: earnings}
}

// DispatchMsg refuses the disabled variants, runs Custom messages through the bindings, and hands
// the rest to the wrapped handler.
func (m Messenger) DispatchMsg(ctx sdk.Context, contract sdk.AccAddress, portID string, msg wasmvmtypes.CosmosMsg) ([]sdk.Event, [][]byte, [][]*codectypes.Any, error) {
	if err := RejectDisabled(msg); err != nil {
		return nil, nil, nil, err
	}
	if msg.Custom == nil {
		return m.next.DispatchMsg(ctx, contract, portID, msg)
	}
	return m.custom(ctx, contract, msg.Custom)
}

// RejectDisabled refuses CosmosMsg variants that would let a contract sign as itself for a module
// that is not behind a binding: staking, CosmosMsg::Any (which is also how the stargate form arrives) and
// SetWithdrawAddress, which would redirect staking rewards to another account.
func RejectDisabled(msg wasmvmtypes.CosmosMsg) error {
	if msg.Any != nil {
		return errorsmod.Wrap(ErrDisabledMessage, "any/stargate: a contract reaches modules only through the orama bindings")
	}
	if msg.Staking != nil {
		return errorsmod.Wrap(ErrDisabledMessage, "staking: contracts do not delegate; the ante-only delegation rules would not apply to them")
	}
	if msg.Distribution != nil && msg.Distribution.SetWithdrawAddress != nil {
		return errorsmod.Wrap(ErrDisabledMessage, "distribution set_withdraw_address")
	}
	return nil
}

func (m Messenger) custom(ctx sdk.Context, contract sdk.AccAddress, raw json.RawMessage) ([]sdk.Event, [][]byte, [][]*codectypes.Any, error) {
	action, err := Decode(contract, raw)
	if err != nil {
		return nil, nil, nil, err
	}
	if action.Pay != nil {
		return m.pay(ctx, contract, action.Pay)
	}
	var (
		events    []sdk.Event
		data      [][]byte
		responses [][]*codectypes.Any
	)
	for _, msg := range action.Msgs {
		res, err := m.route(ctx, msg)
		if err != nil {
			return nil, nil, nil, err
		}
		data = append(data, res.Data)
		responses = append(responses, res.MsgResponses)
		for _, ev := range res.Events {
			events = append(events, sdk.Event(ev))
		}
	}
	return events, data, responses, nil
}

// route validates a message and executes it on its module's message server. The signer is the
// contract by construction (Decode set it), so no signer check is repeated here.
func (m Messenger) route(ctx sdk.Context, msg sdk.Msg) (*sdk.Result, error) {
	if v, ok := msg.(sdk.HasValidateBasic); ok {
		if err := v.ValidateBasic(); err != nil {
			return nil, errorsmod.Wrapf(err, "failed basic validation for %T", msg)
		}
	}
	handler := m.router.Handler(msg)
	if handler == nil {
		return nil, fmt.Errorf("no message server for %T", msg)
	}
	return handler(ctx, msg)
}

func (m Messenger) pay(ctx sdk.Context, contract sdk.AccAddress, p *EarningsPay) ([]sdk.Event, [][]byte, [][]*codectypes.Any, error) {
	recipient, err := sdk.AccAddressFromBech32(p.Recipient)
	if err != nil {
		return nil, nil, nil, ErrBadMessage.Wrapf("earnings recipient: %v", err)
	}
	if p.Amount.IsNil() || !p.Amount.IsPositive() {
		return nil, nil, nil, ErrBadMessage.Wrap("earnings amount must be positive")
	}
	if err := m.earnings.PayEarnings(ctx, contract, recipient, sdk.NewCoin(params.BaseDenom, p.Amount)); err != nil {
		return nil, nil, nil, err
	}
	ev := sdk.NewEvent("orama_earnings_pay",
		sdk.NewAttribute("contract", contract.String()),
		sdk.NewAttribute("recipient", recipient.String()),
		sdk.NewAttribute("amount", p.Amount.String()),
	)
	return []sdk.Event{ev}, nil, nil, nil
}
