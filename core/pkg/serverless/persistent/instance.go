package persistent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DeBrosOfficial/network/pkg/serverless"
	"github.com/tetratelabs/wazero/api"
	"go.uber.org/zap"
)

// WSOpenInput is the JSON payload passed to the WASM module's ws_open export.
// The WASM module unmarshals this from its input buffer.
type WSOpenInput struct {
	ClientID  string            `json:"client_id"`
	Wallet    string            `json:"wallet,omitempty"`
	Namespace string            `json:"namespace"`
	Headers   map[string]string `json:"headers,omitempty"` // filtered subset
}

// CloseReason explains why a connection is shutting down. Passed to ws_close.
type CloseReason string

const (
	CloseReasonClientDisconnect CloseReason = "client_disconnect"
	CloseReasonServerShutdown   CloseReason = "server_shutdown"
	CloseReasonIdleTimeout      CloseReason = "idle_timeout"
	CloseReasonHandlerError     CloseReason = "handler_error"
	CloseReasonRejected         CloseReason = "rejected_by_open"
)

// SendFunc writes a frame to the underlying WebSocket. Returns an error if
// the connection is closed. The instance forwards ws_send hostfunc calls
// to this function.
type SendFunc func(data []byte) error

// Instance owns one WASM module bound to one WebSocket connection.
//
// Lifecycle:
//
//	1. NewInstance — wraps an already-instantiated module
//	2. Open(input) — calls ws_open; non-zero return = reject
//	3. Run(ctx) — drains the inbound channel, calling ws_frame per message;
//	              blocks until ctx cancelled or instance closed
//	4. Submit(frame) — enqueues a frame for Run to pick up; returns error if full
//	5. Close(reason) — calls ws_close; closes the wazero instance; idempotent
type Instance struct {
	clientID     string
	functionName string
	namespace    string

	module   api.Module   // wazero instance, owned by this struct
	openFn   api.Function // exported ws_open
	frameFn  api.Function // exported ws_frame
	closeFn  api.Function // exported ws_close
	allocFn  api.Function // orama_alloc / malloc — for input bytes
	memory   api.Memory

	// Per-instance invocation context. Bound at NewInstance time and
	// attached to every WASM-host call's ctx via
	// hostfunctions.WithInvocationContext. This is what makes persistent
	// WS function_invoke / GetCallerJWTSubject / GetSecret race-free
	// across concurrent connections — each instance carries its own
	// caller identity in the ctx, never reading the HostFunctions
	// singleton field. See pkg/serverless/hostfunctions/invocation_context.go.
	invCtx *serverless.InvocationContext

	inbound chan []byte
	logger  *zap.Logger

	// Per-frame timeout. Bounded by the function's TimeoutSeconds.
	frameTimeout time.Duration

	// Closed exactly once.
	closeOnce sync.Once
	closed    atomic.Bool
}

// Config holds knobs for a persistent instance. Zero values use sensible
// defaults; the gateway populates these from the function's metadata.
type Config struct {
	ClientID          string
	FunctionName      string
	Namespace         string
	FrameTimeoutSec   int // 0 = 30s default
	MaxInflightFrames int // 0 = 64 default

	// InvocationContext is attached to every WASM-host call's ctx so the
	// instance's caller identity (JWT subject, wallet, claims, ws client
	// ID) is race-free across concurrent persistent WS connections.
	//
	// REQUIRED. NewInstance returns an error if nil — without it, host
	// functions would fall back to the shared HostFunctions singleton
	// field and re-open the cross-tenant identity leak this whole
	// machinery exists to fix (see pkg/serverless/invocation_context.go).
	InvocationContext *serverless.InvocationContext
}

// NewInstance wraps an already-instantiated wazero module as a persistent
// instance. Returns an error if any of the required exports
// (ws_open, ws_frame, ws_close) are missing.
//
// The caller retains ownership of the module's lifecycle outside of Close —
// that is, when Close is invoked here, the wazero instance is closed.
func NewInstance(module api.Module, cfg Config, logger *zap.Logger) (*Instance, error) {
	// Reject nil invCtx loud and early. A persistent instance without
	// per-call invCtx propagation falls back to the singleton field on
	// every host call, which races across concurrent connections — the
	// exact bug this design exists to prevent. Caller MUST populate.
	if cfg.InvocationContext == nil {
		return nil, fmt.Errorf("persistent: Config.InvocationContext is required (nil would re-open the cross-tenant identity-leak race; see pkg/serverless/invocation_context.go)")
	}

	openFn := module.ExportedFunction("ws_open")
	if openFn == nil {
		return nil, fmt.Errorf("persistent: module missing ws_open export")
	}
	frameFn := module.ExportedFunction("ws_frame")
	if frameFn == nil {
		return nil, fmt.Errorf("persistent: module missing ws_frame export")
	}
	closeFn := module.ExportedFunction("ws_close")
	if closeFn == nil {
		return nil, fmt.Errorf("persistent: module missing ws_close export")
	}
	allocFn := module.ExportedFunction("orama_alloc")
	if allocFn == nil {
		allocFn = module.ExportedFunction("malloc")
	}
	if allocFn == nil {
		return nil, fmt.Errorf("persistent: module missing orama_alloc/malloc export (required to pass payload bytes)")
	}
	memory := module.Memory()
	if memory == nil {
		return nil, fmt.Errorf("persistent: module exports no memory")
	}

	frameTimeout := time.Duration(cfg.FrameTimeoutSec) * time.Second
	if frameTimeout <= 0 {
		frameTimeout = 30 * time.Second
	}
	maxInflight := cfg.MaxInflightFrames
	if maxInflight <= 0 {
		maxInflight = 64
	}

	return &Instance{
		clientID:     cfg.ClientID,
		functionName: cfg.FunctionName,
		namespace:    cfg.Namespace,
		module:       module,
		openFn:       openFn,
		frameFn:      frameFn,
		closeFn:      closeFn,
		allocFn:      allocFn,
		memory:       memory,
		invCtx:       cfg.InvocationContext,
		inbound:      make(chan []byte, maxInflight),
		logger:       logger,
		frameTimeout: frameTimeout,
	}, nil
}

// withInvCtx returns a derived ctx carrying this instance's invocation
// context. Used by every export call so host functions read identity from
// the per-instance ctx instead of the shared HostFunctions singleton.
//
// Returns ctx unchanged when invCtx is nil — preserves backwards-compat
// for callers that didn't populate Config.InvocationContext.
func (i *Instance) withInvCtx(ctx context.Context) context.Context {
	if i.invCtx == nil {
		return ctx
	}
	return serverless.WithInvocationContext(ctx, i.invCtx)
}

// ClientID returns the WebSocket client ID this instance serves.
func (i *Instance) ClientID() string { return i.clientID }

// Open invokes ws_open with the given input. Returns an error if the WASM
// returns non-zero (connection rejected) or the call traps.
func (i *Instance) Open(ctx context.Context, input WSOpenInput) error {
	payload, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("persistent.Open: marshal input: %w", err)
	}
	ctx, cancel := context.WithTimeout(i.withInvCtx(ctx), i.frameTimeout)
	defer cancel()

	rc, err := i.callExport(ctx, i.openFn, payload)
	if err != nil {
		return fmt.Errorf("persistent.Open: ws_open trap: %w", err)
	}
	if rc != 0 {
		return fmt.Errorf("persistent.Open: ws_open rejected (rc=%d)", rc)
	}
	return nil
}

// Submit enqueues a frame for Run to process. Non-blocking: returns an
// error if the inbound channel is full (caller should drop the connection).
func (i *Instance) Submit(frame []byte) error {
	if i.closed.Load() {
		return fmt.Errorf("persistent.Submit: instance closed")
	}
	select {
	case i.inbound <- frame:
		return nil
	default:
		return fmt.Errorf("persistent.Submit: inbound queue full (max %d)", cap(i.inbound))
	}
}

// Run drains the inbound channel, invoking ws_frame per message. Blocks
// until ctx is cancelled, Close is called, or a frame returns "close".
//
// Frames are processed serially — wazero instances are not goroutine-safe.
func (i *Instance) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case frame, ok := <-i.inbound:
			if !ok {
				return
			}
			if err := i.handleFrame(ctx, frame); err != nil {
				i.logger.Warn("persistent ws_frame error",
					zap.String("client_id", i.clientID),
					zap.String("function", i.functionName),
					zap.Error(err),
				)
				// Frame errors trigger close — caller should disconnect WS.
				return
			}
		}
	}
}

func (i *Instance) handleFrame(ctx context.Context, frame []byte) error {
	frameCtx, cancel := context.WithTimeout(i.withInvCtx(ctx), i.frameTimeout)
	defer cancel()

	rc, err := i.callExport(frameCtx, i.frameFn, frame)
	if err != nil {
		return fmt.Errorf("ws_frame trap: %w", err)
	}
	if rc == 1 {
		return fmt.Errorf("ws_frame requested close")
	}
	return nil
}

// Close invokes ws_close (best-effort, bounded by frameTimeout) then
// shuts down the wazero instance. Idempotent and safe to call multiple times.
func (i *Instance) Close(ctx context.Context, reason CloseReason) {
	i.closeOnce.Do(func() {
		i.closed.Store(true)
		// Drain channel to unblock any blocked Submit on the way out.
		go func() {
			for range i.inbound {
			}
		}()
		// Best-effort ws_close — don't propagate errors; we're shutting down.
		closeCtx, cancel := context.WithTimeout(i.withInvCtx(ctx), i.frameTimeout)
		defer cancel()
		if _, err := i.callExport(closeCtx, i.closeFn, []byte(reason)); err != nil {
			i.logger.Debug("persistent ws_close ignored error",
				zap.String("client_id", i.clientID),
				zap.Error(err),
			)
		}
		close(i.inbound)
		if err := i.module.Close(context.Background()); err != nil {
			i.logger.Debug("persistent module.Close error",
				zap.String("client_id", i.clientID),
				zap.Error(err))
		}
	})
}

// callExport allocates space in the guest, copies `payload` into it, calls
// the export with (ptr, len), then returns the export's first return value
// (or 0 if void).
//
// It does NOT free the allocated memory — the WASM module is short-lived
// at the per-instance level, and we rely on the module being closed at
// session end. For long-running instances with very high frame rates,
// memory growth is bounded by the function's memory_limit_mb.
func (i *Instance) callExport(ctx context.Context, fn api.Function, payload []byte) (uint32, error) {
	var ptr uint64
	if len(payload) > 0 {
		results, err := i.allocFn.Call(ctx, uint64(len(payload)))
		if err != nil {
			return 0, fmt.Errorf("alloc: %w", err)
		}
		ptr = results[0]
		if !i.memory.Write(uint32(ptr), payload) {
			return 0, fmt.Errorf("memory write failed (oom?)")
		}
	}
	results, err := fn.Call(ctx, ptr, uint64(len(payload)))
	if err != nil {
		return 0, err
	}
	if len(results) == 0 {
		return 0, nil
	}
	return uint32(results[0]), nil
}
