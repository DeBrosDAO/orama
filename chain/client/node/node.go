// Package node talks to one oramad through its CometBFT RPC. It runs module
// queries as ABCI queries, reads a block's events, and signs, simulates and
// broadcasts a transaction, then waits until a block includes it. The global
// services (provider, repair delegate, archiver) share it. It holds no key
// material of its own: the caller passes the signing account.
package node

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	gogoproto "github.com/cosmos/gogoproto/proto"

	abci "github.com/cometbft/cometbft/abci/types"
	rpcclient "github.com/cometbft/cometbft/rpc/client"
	rpchttp "github.com/cometbft/cometbft/rpc/client/http"
	coretypes "github.com/cometbft/cometbft/rpc/core/types"

	"cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	txtypes "github.com/cosmos/cosmos-sdk/types/tx"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/client/tx"
	feestypes "github.com/DeBrosOfficial/network/chain/x/fees/types"
)

const (
	// rpcTimeoutSeconds bounds one CometBFT RPC call.
	rpcTimeoutSeconds = 15
	// gasAdjustment scales the simulated gas, in hundredths, so a small
	// state change between simulate and delivery does not run out of gas.
	gasAdjustmentPercent = 150
	// includePoll is how often Submit asks whether its tx is in a block.
	includePoll = time.Second
	// DefaultIncludeTimeout is how long Submit waits for a block to include its tx.
	DefaultIncludeTimeout = time.Minute
)

// ErrNotIncluded is returned when a broadcast tx did not reach a block in time.
// The caller retries with a fresh sequence; the chain state decides whether
// the retry is still needed.
var ErrNotIncluded = errors.New("transaction was not included in a block")

// Client is one oramad CometBFT RPC endpoint and the codec that decodes its
// module answers.
type Client struct {
	rpc            *rpchttp.HTTP
	cdc            codec.Codec
	registry       codectypes.InterfaceRegistry
	txConfig       client.TxConfig
	builder        *tx.Builder
	includeTimeout time.Duration
}

// Dial connects to an oramad RPC address such as tcp://127.0.0.1:31001.
// It does not open a websocket; every read is a request.
func Dial(rpcAddr string) (*Client, error) {
	return DialWith(rpcAddr)
}

// DialWith is Dial for a caller that submits messages of modules the client does not register by
// itself (x/nodes, x/wasm, x/shielded): each function registers one module's interfaces, for
// example nodestypes.RegisterInterfaces.
func DialWith(rpcAddr string, extra ...func(codectypes.InterfaceRegistry)) (*Client, error) {
	if strings.TrimSpace(rpcAddr) == "" {
		return nil, errors.New("chain rpc address is empty")
	}
	rpc, err := rpchttp.NewWithTimeout(rpcAddr, "/websocket", rpcTimeoutSeconds)
	if err != nil {
		return nil, fmt.Errorf("failed to open chain rpc %s: %w", rpcAddr, err)
	}
	regs := make([]register, len(extra))
	for i, fn := range extra {
		regs[i] = fn
	}
	enc, err := newEncoding(regs...)
	if err != nil {
		return nil, err
	}
	builder, err := tx.New(enc.txConfig)
	if err != nil {
		return nil, err
	}
	return &Client{
		rpc: rpc, cdc: enc.cdc, registry: enc.registry, txConfig: enc.txConfig,
		builder: builder, includeTimeout: DefaultIncludeTimeout,
	}, nil
}

// Query runs one gRPC query method as an ABCI query at the latest height.
// method is the full gRPC name, for example /orama.storage.v1.Query/Slot.
func (c *Client) Query(ctx context.Context, method string, req, resp gogoproto.Message) error {
	return c.QueryAt(ctx, 0, method, req, resp)
}

// QueryAt runs one gRPC query method against the state committed at height.
// Height 0 is the latest state. A height whose state the node has pruned is
// an error from the node, not an empty answer.
func (c *Client) QueryAt(ctx context.Context, height int64, method string, req, resp gogoproto.Message) error {
	if height < 0 {
		return fmt.Errorf("query %s: height %d is negative", method, height)
	}
	body, err := gogoproto.Marshal(req)
	if err != nil {
		return fmt.Errorf("encode %s request: %w", method, err)
	}
	res, err := c.rpc.ABCIQueryWithOptions(ctx, method, body, rpcclient.ABCIQueryOptions{Height: height})
	if err != nil {
		return fmt.Errorf("query %s at height %d: %w", method, height, err)
	}
	if res.Response.Code != 0 {
		return &QueryError{Method: method, Codespace: res.Response.Codespace, Code: res.Response.Code, Log: res.Response.Log}
	}
	if err := gogoproto.Unmarshal(res.Response.Value, resp); err != nil {
		return fmt.Errorf("decode %s response: %w", method, err)
	}
	return nil
}

// QueryError is a query the application answered with a non-zero code.
type QueryError struct {
	Method    string
	Codespace string
	Code      uint32
	Log       string
}

func (e *QueryError) Error() string {
	return fmt.Sprintf("query %s failed with code %d: %s", e.Method, e.Code, e.Log)
}

// NotFound reports whether the application said the record does not exist.
// baseapp turns a gRPC NotFound status into the SDK's ErrKeyNotFound.
func (e *QueryError) NotFound() bool {
	return e.Codespace == sdkerrors.ErrKeyNotFound.Codespace() && e.Code == sdkerrors.ErrKeyNotFound.ABCICode()
}

// Status returns the chain id and the latest block height.
func (c *Client) Status(ctx context.Context) (string, int64, error) {
	st, err := c.rpc.Status(ctx)
	if err != nil {
		return "", 0, fmt.Errorf("read chain status: %w", err)
	}
	return st.NodeInfo.Network, st.SyncInfo.LatestBlockHeight, nil
}

// HeightRange returns the earliest block height this node still serves and
// the latest committed height. Blocks below earliest are pruned here.
func (c *Client) HeightRange(ctx context.Context) (int64, int64, error) {
	st, err := c.rpc.Status(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("read chain status: %w", err)
	}
	return st.SyncInfo.EarliestBlockHeight, st.SyncInfo.LatestBlockHeight, nil
}

// LatestHeight returns the latest committed block height.
func (c *Client) LatestHeight(ctx context.Context) (int64, error) {
	_, h, err := c.Status(ctx)
	return h, err
}

// BlockEvents returns the finalize-block events of height and the events of
// every transaction in it that succeeded. A failed transaction's events are
// not state and are left out.
func (c *Client) BlockEvents(ctx context.Context, height int64) ([]abci.Event, error) {
	res, err := c.BlockResults(ctx, height)
	if err != nil {
		return nil, err
	}
	return blockEvents(res), nil
}

// BlockResults returns the finalize-block result of height: one ExecTxResult
// per transaction, in block order, failed ones included.
func (c *Client) BlockResults(ctx context.Context, height int64) (*coretypes.ResultBlockResults, error) {
	res, err := c.rpc.BlockResults(ctx, &height)
	if err != nil {
		return nil, fmt.Errorf("read block results at %d: %w", height, err)
	}
	return res, nil
}

func blockEvents(res *coretypes.ResultBlockResults) []abci.Event {
	events := append([]abci.Event(nil), res.FinalizeBlockEvents...)
	for _, txr := range res.TxsResults {
		if txr == nil || txr.Code != 0 {
			continue
		}
		events = append(events, txr.Events...)
	}
	return events
}

// Block returns the block at height.
func (c *Client) Block(ctx context.Context, height int64) (*coretypes.ResultBlock, error) {
	res, err := c.rpc.Block(ctx, &height)
	if err != nil {
		return nil, fmt.Errorf("read block %d: %w", height, err)
	}
	return res, nil
}

// FeeFunds is what addr can pay a transaction's base fee from: its spendable norama plus its x/fees
// fee-only balance. A node's hot key has only the latter (MsgFundHotKey fills it), so its bank
// balance alone reads zero while it can pay.
func (c *Client) FeeFunds(ctx context.Context, addr string) (math.Int, error) {
	bank, err := c.Balance(ctx, addr)
	if err != nil {
		return math.Int{}, err
	}
	var resp feestypes.QueryFeeBalanceResponse
	if err := c.Query(ctx, "/orama.fees.v1.Query/FeeBalance", &feestypes.QueryFeeBalanceRequest{Address: addr}, &resp); err != nil {
		return math.Int{}, fmt.Errorf("read the fee balance of %s: %w", addr, err)
	}
	if resp.Balance.IsNil() {
		return bank, nil
	}
	return bank.Add(resp.Balance), nil
}

// Balance returns addr's spendable norama.
func (c *Client) Balance(ctx context.Context, addr string) (math.Int, error) {
	var resp banktypes.QueryBalanceResponse
	req := &banktypes.QueryBalanceRequest{Address: addr, Denom: params.BaseDenom}
	if err := c.Query(ctx, "/cosmos.bank.v1beta1.Query/Balance", req, &resp); err != nil {
		return math.Int{}, err
	}
	if resp.Balance == nil {
		return math.ZeroInt(), nil
	}
	return resp.Balance.Amount, nil
}

// account returns the account number and sequence of addr. An account that
// does not exist yet cannot pay a fee, so it is an error that names the fix.
func (c *Client) account(ctx context.Context, addr string) (uint64, uint64, error) {
	var resp authtypes.QueryAccountResponse
	err := c.Query(ctx, "/cosmos.auth.v1beta1.Query/Account", &authtypes.QueryAccountRequest{Address: addr}, &resp)
	if err != nil {
		var qe *QueryError
		if errors.As(err, &qe) && qe.NotFound() {
			return 0, 0, fmt.Errorf("account %s does not exist on chain; fund it before it can sign: %w", addr, err)
		}
		return 0, 0, err
	}
	var acct sdk.AccountI
	if err := c.registry.UnpackAny(resp.Account, &acct); err != nil {
		return 0, 0, fmt.Errorf("decode account %s: %w", addr, err)
	}
	return acct.GetAccountNumber(), acct.GetSequence(), nil
}

// baseFee returns x/fees' current base fee per gas unit.
func (c *Client) baseFee(ctx context.Context) (math.Int, error) {
	var resp feestypes.QueryBaseFeeResponse
	if err := c.Query(ctx, "/orama.fees.v1.Query/BaseFee", &feestypes.QueryBaseFeeRequest{}, &resp); err != nil {
		return math.Int{}, err
	}
	return resp.BaseFee, nil
}

// Submit signs msgs with account, simulates them for gas, pays the base fee
// for that gas with no tip, broadcasts, and waits until a block includes the
// transaction. It returns the tx hash. A CheckTx or DeliverTx failure is an
// error that carries the chain's log.
func (c *Client) Submit(ctx context.Context, account tx.Signer, msgs ...sdk.Msg) (string, error) {
	hash, _, err := c.SubmitWithEvents(ctx, account, msgs...)
	return hash, err
}

// SubmitWithEvents is Submit that also returns the events the transaction emitted, for a caller
// that needs an id the chain assigned (a deal id).
func (c *Client) SubmitWithEvents(ctx context.Context, account tx.Signer, msgs ...sdk.Msg) (string, []abci.Event, error) {
	if len(msgs) == 0 {
		return "", nil, errors.New("no messages to submit")
	}
	chainID, _, err := c.Status(ctx)
	if err != nil {
		return "", nil, err
	}
	number, seq, err := c.account(ctx, account.AccountAddress())
	if err != nil {
		return "", nil, err
	}
	unsigned := tx.Unsigned{
		ChainID: chainID, AccountNumber: number, Sequence: seq,
		Fee: sdk.NewCoins(sdk.NewInt64Coin(params.BaseDenom, 1)), Msgs: msgs,
	}
	gas, err := c.simulate(ctx, account, unsigned)
	if err != nil {
		return "", nil, err
	}
	base, err := c.baseFee(ctx)
	if err != nil {
		return "", nil, err
	}
	unsigned.GasLimit = gas
	unsigned.Fee = sdk.NewCoins(sdk.NewCoin(params.BaseDenom, feeFor(gas, base)))
	raw, err := c.builder.BuildWith(account, unsigned)
	if err != nil {
		return "", nil, fmt.Errorf("sign transaction: %w", err)
	}
	return c.broadcast(ctx, raw)
}

// SubmitSignerless broadcasts msg as a transaction with no signature and no fee, declaring exactly
// gas, and waits until a block includes it. It is the path of a message the protocol admits without
// a signer (x/shielded's MsgShieldedTransfer, which must be alone in its transaction); the chain
// refuses any other message sent this way.
func (c *Client) SubmitSignerless(ctx context.Context, gas uint64, msg sdk.Msg) (string, []abci.Event, error) {
	b := c.txConfig.NewTxBuilder()
	if err := b.SetMsgs(msg); err != nil {
		return "", nil, fmt.Errorf("build the transaction: %w", err)
	}
	b.SetGasLimit(gas)
	raw, err := c.txConfig.TxEncoder()(b.GetTx())
	if err != nil {
		return "", nil, fmt.Errorf("encode the transaction: %w", err)
	}
	return c.broadcast(ctx, raw)
}

// feeFor is gas times the base fee, and at least one norama so the fee is a
// valid positive coin.
func feeFor(gas uint64, base math.Int) math.Int {
	fee := base.Mul(math.NewIntFromUint64(gas))
	if fee.IsPositive() {
		return fee
	}
	return math.OneInt()
}

func (c *Client) simulate(ctx context.Context, account tx.Signer, unsigned tx.Unsigned) (uint64, error) {
	raw, err := c.builder.BuildWith(account, unsigned)
	if err != nil {
		return 0, fmt.Errorf("sign transaction for simulation: %w", err)
	}
	var resp txtypes.SimulateResponse
	if err := c.Query(ctx, "/cosmos.tx.v1beta1.Service/Simulate", &txtypes.SimulateRequest{TxBytes: raw}, &resp); err != nil {
		return 0, fmt.Errorf("simulate transaction: %w", err)
	}
	if resp.GasInfo == nil || resp.GasInfo.GasUsed == 0 {
		return 0, errors.New("simulation reported no gas used")
	}
	return resp.GasInfo.GasUsed * gasAdjustmentPercent / 100, nil
}

func (c *Client) broadcast(ctx context.Context, raw []byte) (string, []abci.Event, error) {
	res, err := c.rpc.BroadcastTxSync(ctx, raw)
	if err != nil {
		return "", nil, fmt.Errorf("broadcast transaction: %w", err)
	}
	hash := res.Hash.String()
	if res.Code != 0 {
		return hash, nil, fmt.Errorf("transaction %s rejected by CheckTx (code %d): %s", hash, res.Code, res.Log)
	}
	deadline := time.NewTimer(c.includeTimeout)
	defer deadline.Stop()
	tick := time.NewTicker(includePoll)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return hash, nil, ctx.Err()
		case <-deadline.C:
			return hash, nil, fmt.Errorf("%w: %s after %s", ErrNotIncluded, hash, c.includeTimeout)
		case <-tick.C:
			got, err := c.rpc.Tx(ctx, res.Hash, false)
			if err != nil {
				// The tx indexer answers "not found" until a block includes it.
				// Anything else (RPC down, indexing disabled) is the real cause.
				if strings.Contains(err.Error(), "not found") {
					continue
				}
				return hash, nil, fmt.Errorf("look up transaction %s: %w", hash, err)
			}
			if got.TxResult.Code != 0 {
				return hash, nil, fmt.Errorf("transaction %s failed in block %d (code %d): %s", hash, got.Height, got.TxResult.Code, got.TxResult.Log)
			}
			return hash, got.TxResult.Events, nil
		}
	}
}
