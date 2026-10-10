//go:build cgo && !nowasm

package app

import (
	"context"
	"encoding/json"
	"fmt"
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

	marketkeeper "github.com/DeBrosOfficial/network/chain/x/market/keeper"
	tokenkeeper "github.com/DeBrosOfficial/network/chain/x/token/keeper"
	"github.com/DeBrosOfficial/network/chain/x/wasmbindings"
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

	var wasmK *wasmkeeper.Keeper
	isContract := func(ctx context.Context, addr sdk.AccAddress) bool {
		return wasmK.HasContractInfo(ctx, addr) || wasmpolicy.IsFundedContract(ctx, addr)
	}
	app.isContract = isContract
	querier := wasmbindings.NewQuerier(
		tokenkeeper.NewQueryServerImpl(app.TokenKeeper),
		app.CnftKeeper,
		marketkeeper.NewQueryServerImpl(app.MarketKeeper),
	)

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
		wasmkeeper.WithMessageHandlerDecorator(func(old wasmkeeper.Messenger) wasmkeeper.Messenger {
			return wasmbindings.NewMessenger(rejectContractIBC(old), app.MsgServiceRouter(), app.FeesKeeper)
		}),
		wasmkeeper.WithQueryPlugins(&wasmkeeper.QueryPlugins{Custom: querier.Query}),
		wasmkeeper.WithWasmEngineDecorator(func(old wasmtypes.WasmEngine) wasmtypes.WasmEngine {
			return newDepositEngine(old, app.WasmPolicyKeeper, isContract)
		}),
	)
	keeper := &k
	wasmK = keeper
	app.tokenHook.exists = wasmK.HasContractInfo
	app.tokenHook.sudo = func(ctx context.Context, contract sdk.AccAddress, msg []byte) error {
		_, err := wasmkeeper.NewDefaultPermissionKeeper(wasmK).Sudo(sdk.UnwrapSDKContext(ctx), contract, msg)
		return err
	}
	app.wasmKeeper = keeper
	app.contractSend = ante.NewContractSendDecorator(isContract, moduleAccountNames())
	//lint:ignore SA1019 module.NewManager accepts only the legacy module.AppModule; the modules are wired through it
	app.wasmModules = []module.AppModule{
		policyModule(app.WasmPolicyKeeper),
		wasm.NewAppModule(app.appCodec, keeper, app.StakingKeeper, app.AccountKeeper, app.BankKeeper, app.MsgServiceRouter(), nil),
	}
	app.wasmGenesisOrder = []string{policytypes.ModuleName, wasmtypes.ModuleName}
}

// registerWasmSnapshot adds contract bytecode to state-sync snapshots. The IAVL snapshot carries
// only each code's CodeInfo; the bytecode lives in <home>/wasm, so without this a state-synced
// node would hold contracts it cannot execute and diverge at the first transaction that calls one.
func (app *OramaApp) registerWasmSnapshot() {
	manager := app.SnapshotManager()
	if manager == nil {
		return
	}
	if err := manager.RegisterExtensions(wasmkeeper.NewWasmSnapshotter(app.CommitMultiStore(), app.WasmKeeper())); err != nil {
		panic(fmt.Errorf("register the wasm snapshot extension: %w", err))
	}
}

// WasmKeeper is the wasmd keeper, for queries and tests.
func (app *OramaApp) WasmKeeper() *wasmkeeper.Keeper {
	return app.wasmKeeper.(*wasmkeeper.Keeper)
}

// WasmContractKeeper is the wasmd permissioned keeper, used by the cgo instantiate test.
func (app *OramaApp) WasmContractKeeper() *wasmkeeper.PermissionedKeeper {
	k := app.wasmKeeper.(*wasmkeeper.Keeper)
	return wasmkeeper.NewDefaultPermissionKeeper(k)
}

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
	// wasmd calls this only to fund a contract, instantiate or execute, and for instantiate the
	// contract is not registered yet: tell the norama restrictions the recipient is one.
	return t.inner.TransferCoins(wasmpolicy.WithFundedContract(ctx, to), from, to, amt)
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
