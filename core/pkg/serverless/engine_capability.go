package serverless

import (
	"context"
	"time"

	"github.com/tetratelabs/wazero/api"
	"go.uber.org/zap"
)

// The capability host calls (feat-264). hostfunctions/capability.go says what
// each one does; these move bytes across the guest boundary.

// maxCapabilityTTLSeconds bounds a guest's ttl before it becomes a Duration, so
// no value it passes can overflow one. The issuer enforces the real bounds.
const maxCapabilityTTLSeconds = int64(365 * 24 * 60 * 60)

// hCapabilityMint mints a capability for the calling function's WebSocket.
// Returns the packed ptr<<32|len of the JSON
// {"token","cap_id","resource","issuer_device","expires_at"}, or 0 on failure,
// whose reason the gateway log records.
func (e *Engine) hCapabilityMint(ctx context.Context, mod api.Module, resPtr, resLen uint32, ttlSeconds int64) uint64 {
	resource, ok := e.executor.ReadFromGuest(mod, resPtr, resLen)
	if !ok {
		return 0
	}
	if ttlSeconds <= 0 || ttlSeconds > maxCapabilityTTLSeconds {
		e.logger.Warn("host function capability_mint refused: ttl out of range", zap.Int64("ttl_seconds", ttlSeconds))
		return 0
	}
	minted, err := e.hostServices.MintCapability(ctx, string(resource), time.Duration(ttlSeconds)*time.Second)
	if err != nil {
		e.logger.Warn("host function capability_mint refused", zap.Int64("ttl_seconds", ttlSeconds), zap.Error(err))
		return 0
	}
	return e.executor.WriteToGuest(ctx, mod, []byte(minted))
}

// hCapabilityRevoke revokes one capability of the calling function's
// namespace, named by its token. Returns 1 on success, 0 on failure.
func (e *Engine) hCapabilityRevoke(ctx context.Context, mod api.Module, tokenPtr, tokenLen uint32) uint32 {
	token, ok := e.executor.ReadFromGuest(mod, tokenPtr, tokenLen)
	if !ok {
		return 0
	}
	if err := e.hostServices.RevokeCapability(ctx, string(token)); err != nil {
		e.logger.Error("host function capability_revoke failed", zap.Error(err))
		return 0
	}
	return 1
}

// hGetCallerCapability returns what the capability the caller's socket was
// opened with grants, as JSON, or an empty string.
func (e *Engine) hGetCallerCapability(ctx context.Context, mod api.Module) uint64 {
	return e.executor.WriteToGuest(ctx, mod, []byte(e.hostServices.GetCallerCapability(ctx)))
}
