package pubsub

import (
    "fmt"
    "go.uber.org/zap"
)

// Logf is a package-level logger function used by pubsub internals.
// By default it is a no-op to avoid polluting stdout; applications can
// assign it (e.g., to a UI-backed logger) to surface logs as needed.
var Logf = func(format string, args ...interface{}) { _ = fmt.Sprintf(format, args...) }

// SetLogFunc allows applications to provide a custom logger sink.
func SetLogFunc(f func(string, ...interface{})) { if f != nil { Logf = f } }

// newPubSubLogger creates a zap.Logger for pubsub components.
// Quiet mode can be handled by callers by using production config externally;
// here we default to development logger for richer diagnostics during dev.
func newPubSubLogger(quiet bool) (*zap.Logger, error) {
    if quiet {
        cfg := zap.NewProductionConfig()
        cfg.Level = zap.NewAtomicLevelAt(zap.WarnLevel)
        cfg.DisableCaller = true
        cfg.DisableStacktrace = true
        return cfg.Build()
    }
    return zap.NewDevelopment()
}
