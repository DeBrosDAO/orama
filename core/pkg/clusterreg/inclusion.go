package clusterreg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/httputil"
)

const (
	// InclusionTimeout is how long a submission waits for its transaction to be in a block. A
	// block takes seconds; a transaction that is not in one after this long is reported, not
	// waited on forever.
	InclusionTimeout = 2 * time.Minute
	// InclusionPoll is how often the chain is asked whether the transaction is in a block.
	InclusionPoll = time.Second
	// maxResultLog is how many characters of the chain's log a failure quotes.
	maxResultLog = 300
)

var txHashPattern = regexp.MustCompile(`^[0-9A-Fa-f]{64}$`)

// printable is httputil.Printable: text the chain sent is printed to the operator's terminal.
func printable(s string) string { return httputil.Printable(s) }

// ErrNotIncluded is returned when a broadcast transaction is not in a block by the deadline.
var ErrNotIncluded = errors.New("the transaction is not in a block")

// WaitIncluded asks the chain REST API at base for the transaction hash every poll until it is in
// a block, and returns that block's height. A broadcast only admits a transaction to the mempool:
// it can still fail when its block runs it, and that failure is returned here with the chain's log.
// Not found means not in a block yet; any other error ends the wait.
func WaitIncluded(ctx context.Context, base, hash string, timeout, poll time.Duration) (int64, error) {
	// The hash came back from the node that took the broadcast: only a transaction hash may go
	// into the lookup's path.
	if !txHashPattern.MatchString(hash) {
		return 0, fmt.Errorf("the chain returned %q as the transaction hash, which is not a 64-digit hex hash", printable(hash))
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	tick := time.NewTicker(poll)
	defer tick.Stop()
	url := strings.TrimRight(base, "/") + "/cosmos/tx/v1beta1/txs/" + hash
	for {
		height, found, err := txResult(ctx, url, hash)
		if ctx.Err() != nil {
			// The wait ended while a lookup was in flight: that is the deadline or the caller's
			// cancellation, not a failed lookup.
			return 0, waitEnded(ctx, timeout, hash)
		}
		if err != nil || found {
			return height, err
		}
		select {
		case <-ctx.Done():
			return 0, waitEnded(ctx, timeout, hash)
		case <-tick.C:
		}
	}
}

func waitEnded(ctx context.Context, timeout time.Duration, hash string) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%w after %s: %s", ErrNotIncluded, timeout, hash)
	}
	return ctx.Err()
}

// txResult reads one transaction result. found is false while the chain does not have it.
func txResult(ctx context.Context, url, hash string) (int64, bool, error) {
	body, err := getJSON(ctx, url)
	var status *StatusError
	switch {
	case errors.As(err, &status) && status.Code == http.StatusNotFound:
		return 0, false, nil
	case err != nil:
		return 0, false, fmt.Errorf("read the transaction result: %w", err)
	}
	var resp struct {
		TxResponse struct {
			Height string `json:"height"`
			TxHash string `json:"txhash"`
			Code   uint32 `json:"code"`
			RawLog string `json:"raw_log"`
		} `json:"tx_response"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, false, fmt.Errorf("transaction result is not JSON")
	}
	// The answer must be about the transaction asked for: a node that answers another one's block
	// and result would have this one reported as included, or as failed, on that word.
	if !strings.EqualFold(resp.TxResponse.TxHash, hash) {
		return 0, false, fmt.Errorf("asked for transaction %s and the node answered the result of %q; refusing to report it", hash, printable(resp.TxResponse.TxHash))
	}
	height, err := strconv.ParseInt(resp.TxResponse.Height, 10, 64)
	if err != nil || height <= 0 {
		return 0, false, fmt.Errorf("transaction result has no block height (%q)", resp.TxResponse.Height)
	}
	if resp.TxResponse.Code != 0 {
		log := []rune(printable(resp.TxResponse.RawLog))
		if len(log) > maxResultLog {
			log = log[:maxResultLog]
		}
		return height, true, fmt.Errorf("the transaction failed in block %d (code %d): %s", height, resp.TxResponse.Code, string(log))
	}
	return height, true, nil
}
