//go:build cgo && !nowasm

package app

import (
	"context"
	"encoding/json"
	"os"

	wasmvmtypes "github.com/CosmWasm/wasmvm/v3/types"
	"github.com/spf13/cast"

	clienttypes "github.com/cosmos/ibc-go/v11/modules/core/02-client/types"
	channeltypes "github.com/cosmos/ibc-go/v11/modules/core/04-channel/types"
	channeltypesv2 "github.com/cosmos/ibc-go/v11/modules/core/04-channel/v2/types"
	ibcexported "github.com/cosmos/ibc-go/v11/modules/core/exported"

	"github.com/cosmos/cosmos-sdk/client/flags"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/runtime"
	servertypes "github.com/cosmos/cosmos-sdk/server/types"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	distrkeeper "github.com/cosmos/cosmos-sdk/x/distribution/keeper"

	"github.com/CosmWasm/wasmd/x/wasm"
	wasmkeeper "github.com/CosmWasm/wasmd/x/wasm/keeper"
	wasmtypes "github.com/CosmWasm/wasmd/x/wasm/types"

	"github.com/DeBrosOfficial/network/chain/x/wasmpolicy"
	"github.com/DeBrosOfficial/network/chain/x/wasmpolicy/ante"
	policytypes "github.com/DeBrosOfficial/network/chain/x/wasmpolicy/types"
)

func wasmModuleAccountPerms() map[string][]string {
	return map[string][]string{
		wasmtypes.ModuleName: {authtypes.Burner},
	}
}

// WasmVMLinked reports whether this binary links libwasmvm.
func WasmVMLinked() bool { return true }

func (app *OramaApp) installWasm(keys map[string]*storetypes.KVStoreKey, appOpts servertypes.AppOptions) {
	app.mountWasmPolicy(keys)
	if _, ok := keys[wasmtypes.StoreKey]; !ok {
		keys[wasmtypes.StoreKey] = storetypes.NewKVStoreKey(wasmtypes.StoreKey)
	}

	home := cast.ToString(appOpts.Get(flags.FlagHome))
	if home == "" {
		var err error
		home, err = os.MkdirTemp("", "oramad-wasm-*")
		if err != nil {
			panic(err)
		}
	}
	nodeConfig, err := wasm.ReadNodeConfig(appOpts)
	if err != nil {
		panic(err)
	}

	k := wasmkeeper.NewKeeper(
		app.appCodec,
		runtime.NewKVStoreService(keys[wasmtypes.StoreKey]),
		app.AccountKeeper,
		app.BankKeeper,
		app.StakingKeeper,
		distrkeeper.NewQuerier(app.DistrKeeper),
		wasmpolicy.ICS4Noop{},
		noopChannel{},
		noopChannelV2{},
		noopPort{},
		app.MsgServiceRouter(),
		app.GRPCQueryRouter(),
		home,
		nodeConfig,
		wasmtypes.VMConfig{},
		WasmCapabilities(),
		UnreachableAuthority(),
		wasmkeeper.WithCoinTransferrer(newAllowModuleTransferrer(app.BankKeeper)),
		wasmkeeper.WithMessageHandlerDecorator(rejectContractIBC),
	)
	keeper := &k
	app.wasmKeeper = keeper
	app.contractSend = ante.NewFeeEarningsDecorator(func(ctx context.Context, addr sdk.AccAddress) bool {
		return keeper.HasContractInfo(ctx, addr)
	})
	app.wasmModules = []module.AppModule{
		policyModule(app.WasmPolicyKeeper),
		wasm.NewAppModule(app.appCodec, keeper, app.StakingKeeper, app.AccountKeeper, app.BankKeeper, app.MsgServiceRouter(), nil),
	}
	app.wasmGenesisOrder = []string{policytypes.ModuleName, wasmtypes.ModuleName}
}

// WasmContractKeeper is the wasmd permissioned keeper, used by the cgo instantiate test.
func (app *OramaApp) WasmContractKeeper() *wasmkeeper.PermissionedKeeper {
	k := app.wasmKeeper.(*wasmkeeper.Keeper)
	return wasmkeeper.NewDefaultPermissionKeeper(k)
}

func guardWasmClaim(servertypes.AppOptions) error { return nil }

func guardWasmGenesis(map[string]json.RawMessage) error { return nil }

// noopChannel satisfies wasmd's ChannelKeeper. Every method refuses IBC.
type noopChannel struct{}

func (noopChannel) GetChannel(sdk.Context, string, string) (channeltypes.Channel, bool) {
	return channeltypes.Channel{}, false
}

func (noopChannel) GetNextSequenceSend(sdk.Context, string, string) (uint64, bool) {
	return 0, false
}

func (noopChannel) ChanCloseInit(sdk.Context, string, string) error {
	return wasmpolicy.RejectContractIBC(wasmpolicy.ContractIBC{CloseChannel: true})
}

func (noopChannel) GetAllChannels(sdk.Context) []channeltypes.IdentifiedChannel { return nil }

func (noopChannel) SetChannel(sdk.Context, string, string, channeltypes.Channel) {}

func (noopChannel) GetAllChannelsWithPortPrefix(sdk.Context, string) []channeltypes.IdentifiedChannel {
	return nil
}

func (noopChannel) SendPacket(sdk.Context, string, string, clienttypes.Height, uint64, []byte) (uint64, error) {
	return 0, wasmpolicy.RejectContractIBC(wasmpolicy.ContractIBC{SendPacket: true})
}

func (noopChannel) WriteAcknowledgement(sdk.Context, ibcexported.PacketI, ibcexported.Acknowledgement) error {
	return wasmpolicy.RejectContractIBC(wasmpolicy.ContractIBC{SendPacket: true})
}

func (noopChannel) GetAppVersion(sdk.Context, string, string) (string, bool) { return "", false }

type noopChannelV2 struct{}

func (noopChannelV2) WriteAcknowledgement(sdk.Context, string, uint64, channeltypesv2.Acknowledgement) error {
	return wasmpolicy.RejectContractIBC(wasmpolicy.ContractIBC{SendPacket: true})
}

func (noopChannelV2) GetAsyncPacket(sdk.Context, string, uint64) (channeltypesv2.Packet, bool) {
	return channeltypesv2.Packet{}, false
}

type noopPort struct{}

func (noopPort) GetPort(sdk.Context) string { return "" }

type allowModuleTransferrer struct {
	inner wasmkeeper.CoinTransferrer
	bank  wasmtypes.BankKeeper
	allow []sdk.AccAddress
}

func newAllowModuleTransferrer(bank wasmtypes.BankKeeper) allowModuleTransferrer {
	allow := make([]sdk.AccAddress, 0, len(wasmpolicy.FeeEarningsModules()))
	for _, name := range wasmpolicy.FeeEarningsModules() {
		allow = append(allow, authtypes.NewModuleAddress(name))
	}
	return allowModuleTransferrer{
		inner: wasmkeeper.NewBankCoinTransferrer(bank),
		bank:  bank,
		allow: allow,
	}
}

func (t allowModuleTransferrer) TransferCoins(ctx sdk.Context, from, to sdk.AccAddress, amt sdk.Coins) error {
	for _, allowed := range t.allow {
		if allowed.Equals(to) {
			// Skip the stock BlockedAddr check. SendCoins still runs the norama restriction.
			return t.bank.SendCoins(ctx, from, to, amt)
		}
	}
	return t.inner.TransferCoins(ctx, from, to, amt)
}

type ibcRejectMessenger struct {
	next wasmkeeper.Messenger
}

func rejectContractIBC(old wasmkeeper.Messenger) wasmkeeper.Messenger {
	return ibcRejectMessenger{next: old}
}

func (m ibcRejectMessenger) DispatchMsg(ctx sdk.Context, contract sdk.AccAddress, portID string, msg wasmvmtypes.CosmosMsg) ([]sdk.Event, [][]byte, [][]*codectypes.Any, error) {
	if msg.IBC != nil || msg.IBC2 != nil {
		return nil, nil, nil, wasmpolicy.RejectContractIBC(wasmpolicy.ContractIBC{OpenChannel: true, SendPacket: true})
	}
	return m.next.DispatchMsg(ctx, contract, portID, msg)
}

var (
	_ wasmtypes.ChannelKeeper           = noopChannel{}
	_ wasmtypes.ChannelKeeperV2         = noopChannelV2{}
	_ wasmtypes.ICS20TransferPortSource = noopPort{}
)
