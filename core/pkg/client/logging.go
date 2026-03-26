package client

import (
	"go.uber.org/zap"
)

// newClientLogger creates a zap.Logger based on quiet mode preference.
// Quiet mode returns a production logger with Warn+ level and reduced noise.
// Non-quiet returns a development logger with debug/info output.
func newClientLogger(quiet bool) (*zap.Logger, error) {
	if quiet {
		cfg := zap.NewProductionConfig()
		cfg.Level = zap.NewAtomicLevelAt(zap.WarnLevel)
		cfg.DisableCaller = true
		cfg.DisableStacktrace = true
		return cfg.Build()
	}
	return zap.NewDevelopment()
}
