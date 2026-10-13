// Package broadcast sends pre-signed transactions straight to CometBFT's RPC and keeps the sending
// honest: an account's transactions leave in sequence order through one node, the offered rate is
// the one asked for, and a refusal is recorded instead of retried.
package broadcast

import (
	"context"
	"fmt"
	"strings"

	rpchttp "github.com/cometbft/cometbft/rpc/client/http"
)

// Mode is which broadcast call a sender uses.
type Mode string

const (
	// ModeSync is broadcast_tx_sync: the call returns after CheckTx, so a refusal is seen.
	ModeSync Mode = "sync"
	// ModeAsync is broadcast_tx_async: the call returns at once, before CheckTx. A refused
	// transaction is never seen as refused; it just never appears in a block.
	ModeAsync Mode = "async"
)

// ParseMode reads a mode name.
func ParseMode(s string) (Mode, error) {
	switch Mode(strings.ToLower(s)) {
	case ModeSync:
		return ModeSync, nil
	case ModeAsync:
		return ModeAsync, nil
	}
	return "", fmt.Errorf("broadcast mode %q is not %q or %q", s, ModeSync, ModeAsync)
}

// CheckResult is the node's answer to a broadcast.
type CheckResult struct {
	Code uint32
	Log  string
}

// Sender broadcasts one transaction to one node.
type Sender interface {
	Send(ctx context.Context, raw []byte) (CheckResult, error)
}

// RPCSender broadcasts through a CometBFT RPC client.
type RPCSender struct {
	Client *rpchttp.HTTP
	Mode   Mode
}

// Send broadcasts raw with the sender's mode.
func (s RPCSender) Send(ctx context.Context, raw []byte) (CheckResult, error) {
	if s.Mode == ModeAsync {
		if _, err := s.Client.BroadcastTxAsync(ctx, raw); err != nil {
			return CheckResult{}, fmt.Errorf("broadcast_tx_async: %w", err)
		}
		return CheckResult{}, nil
	}
	res, err := s.Client.BroadcastTxSync(ctx, raw)
	if err != nil {
		return CheckResult{}, fmt.Errorf("broadcast_tx_sync: %w", err)
	}
	return CheckResult{Code: res.Code, Log: res.Log}, nil
}
