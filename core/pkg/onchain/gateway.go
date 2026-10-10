package onchain

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/DeBrosOfficial/network/pkg/chainread"
	"github.com/DeBrosOfficial/network/pkg/clusterreg"
	"github.com/DeBrosOfficial/network/pkg/httputil"
)

const (
	queryAccountInfo = "cosmos.auth.v1beta1.Query/AccountInfo"
	queryBaseFee     = "orama.fees.v1.Query/BaseFee"
	// txHashBytes is the size of a transaction hash.
	txHashBytes = 32
	// maxResultLog is how many characters of the chain's log a failure quotes.
	maxResultLog = 300
)

// Gateway is a Chain backed by a gateway's public /v1/chain/ routes: the account and fee reads of
// the wallet query route, POST /v1/chain/simulate and /broadcast, and GET /v1/chain/tx. A wallet
// with no node of its own sends its transactions through it.
type Gateway struct{ Reader *chainread.Reader }

func (g Gateway) Account(ctx context.Context, address string) (clusterreg.Account, error) {
	request, err := json.Marshal(map[string]string{"address": address})
	if err != nil {
		return clusterreg.Account{}, fmt.Errorf("encode the account request: %w", err)
	}
	raw, err := g.Reader.GatewayQuery(ctx, queryAccountInfo, string(request))
	if err != nil {
		return clusterreg.Account{}, statusAsClusterreg(err)
	}
	return parseAccountInfo(raw)
}

func parseAccountInfo(raw json.RawMessage) (clusterreg.Account, error) {
	var resp struct {
		Info struct {
			PubKey *struct {
				Key string `json:"key"`
			} `json:"pub_key"`
			AccountNumber string `json:"account_number"`
			Sequence      string `json:"sequence"`
		} `json:"info"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return clusterreg.Account{}, fmt.Errorf("the gateway answered an account that is not JSON: %w", err)
	}
	number, err := strconv.ParseUint(resp.Info.AccountNumber, 10, 64)
	if err != nil {
		return clusterreg.Account{}, fmt.Errorf("the gateway answered account_number %q: %w", resp.Info.AccountNumber, err)
	}
	sequence, err := strconv.ParseUint(resp.Info.Sequence, 10, 64)
	if err != nil {
		return clusterreg.Account{}, fmt.Errorf("the gateway answered sequence %q: %w", resp.Info.Sequence, err)
	}
	acct := clusterreg.Account{Number: number, Sequence: sequence}
	if resp.Info.PubKey != nil && resp.Info.PubKey.Key != "" {
		pub, err := base64.StdEncoding.DecodeString(resp.Info.PubKey.Key)
		if err != nil || len(pub) != compressedKeyLen {
			return clusterreg.Account{}, errors.New("the gateway answered an account public key that is not a 33-byte key")
		}
		acct.PubKey = pub
	}
	return acct, nil
}

// compressedKeyLen is the size of a compressed secp256k1 public key.
const compressedKeyLen = 33

// statusAsClusterreg turns the gateway's HTTP status into the status error accountError looks for,
// so a missing account reads the same through a gateway as through a node.
func statusAsClusterreg(err error) error {
	var status *chainread.StatusError
	if errors.As(err, &status) {
		return fmt.Errorf("%w: %w", &clusterreg.StatusError{Code: status.Code}, err)
	}
	return err
}

func (g Gateway) BaseFee(ctx context.Context) (string, error) {
	raw, err := g.Reader.GatewayQuery(ctx, queryBaseFee, "{}")
	if err != nil {
		return "", fmt.Errorf("read the base fee from the gateway: %w", err)
	}
	var resp struct {
		BaseFee string `json:"base_fee"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", fmt.Errorf("the gateway answered a base fee that is not JSON: %w", err)
	}
	if _, err := ParseBaseFee(resp.BaseFee); err != nil {
		return "", fmt.Errorf("the gateway answered a base fee that cannot be used: %w", err)
	}
	return resp.BaseFee, nil
}

func (g Gateway) SimulateGas(ctx context.Context, tx []byte) (uint64, error) {
	res, err := g.Reader.Simulate(ctx, tx)
	if err != nil {
		return 0, err
	}
	if res.GasUsed == 0 {
		return 0, errors.New("the simulation reported no gas used")
	}
	return res.GasUsed, nil
}

func (g Gateway) Broadcast(ctx context.Context, tx []byte) (string, error) {
	res, err := g.Reader.Broadcast(ctx, tx)
	if err != nil {
		return "", err
	}
	if res.Code != 0 {
		return "", &chainread.TxRefusedError{Code: res.Code, Codespace: res.Codespace, Log: res.Log, TxHash: res.TxHash}
	}
	if res.TxHash == "" {
		return "", errors.New("the gateway accepted the transaction and returned no hash")
	}
	return res.TxHash, nil
}

// WaitIncluded polls the gateway for the transaction until it is in a block, and returns that
// block's height. A transaction the block refused is returned with the chain's log.
func (g Gateway) WaitIncluded(ctx context.Context, hash string) (int64, error) {
	if raw, err := hex.DecodeString(hash); err != nil || len(raw) != txHashBytes {
		return 0, fmt.Errorf("the chain returned %q as the transaction hash, which is not a 64-digit hex hash", httputil.Printable(hash))
	}
	ctx, cancel := context.WithTimeout(ctx, clusterreg.InclusionTimeout)
	defer cancel()
	tick := time.NewTicker(clusterreg.InclusionPoll)
	defer tick.Stop()
	for {
		height, found, err := g.txResult(ctx, hash)
		if ctx.Err() != nil {
			return 0, waitEnded(ctx, hash)
		}
		if err != nil || found {
			return height, err
		}
		select {
		case <-ctx.Done():
			return 0, waitEnded(ctx, hash)
		case <-tick.C:
		}
	}
}

func waitEnded(ctx context.Context, hash string) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%w after %s: %s", clusterreg.ErrNotIncluded, clusterreg.InclusionTimeout, hash)
	}
	return ctx.Err()
}

// txResult reads one transaction. found is false while the gateway answers 404.
func (g Gateway) txResult(ctx context.Context, hash string) (height int64, found bool, err error) {
	raw, err := g.Reader.GatewayGet(ctx, "tx?hash="+url.QueryEscape(hash))
	var status *chainread.StatusError
	switch {
	case errors.As(err, &status) && status.Code == http.StatusNotFound:
		return 0, false, nil
	case err != nil:
		return 0, false, fmt.Errorf("read the transaction result: %w", err)
	}
	var resp struct {
		Result struct {
			Height   string `json:"height"`
			TxResult struct {
				Code uint32 `json:"code"`
				Log  string `json:"log"`
			} `json:"tx_result"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return 0, false, fmt.Errorf("the gateway answered a transaction result that is not JSON: %w", err)
	}
	height, err = strconv.ParseInt(resp.Result.Height, 10, 64)
	if err != nil || height <= 0 {
		return 0, false, fmt.Errorf("the transaction result has no block height (%q)", resp.Result.Height)
	}
	if resp.Result.TxResult.Code != 0 {
		return height, true, fmt.Errorf("the transaction failed in block %d (code %d): %s",
			height, resp.Result.TxResult.Code, quoteLog(resp.Result.TxResult.Log))
	}
	return height, true, nil
}

// quoteLog bounds the chain's log and drops what would act on a terminal.
func quoteLog(log string) string { return httputil.PrintableMax(log, maxResultLog) }
