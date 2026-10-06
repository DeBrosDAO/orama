package execution

import (
	"context"
	"fmt"

	"github.com/tetratelabs/wazero"
	"go.uber.org/zap"
)

// ModuleLifecycle manages the lifecycle of WASM modules.
type ModuleLifecycle struct {
	runtime wazero.Runtime
	logger  *zap.Logger
}

// NewModuleLifecycle creates a new ModuleLifecycle manager.
func NewModuleLifecycle(runtime wazero.Runtime, logger *zap.Logger) *ModuleLifecycle {
	return &ModuleLifecycle{
		runtime: runtime,
		logger:  logger,
	}
}

// CompileModule compiles WASM bytecode into a compiled module.
func (m *ModuleLifecycle) CompileModule(ctx context.Context, wasmCID string, wasmBytes []byte) (wazero.CompiledModule, error) {
	if len(wasmBytes) == 0 {
		return nil, fmt.Errorf("WASM bytes cannot be empty")
	}

	compiled, err := m.runtime.CompileModule(ctx, wasmBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to compile WASM module %s: %w", wasmCID, err)
	}

	m.logger.Debug("Module compiled successfully",
		zap.String("wasm_cid", wasmCID),
		zap.Int("size_bytes", len(wasmBytes)),
	)

	return compiled, nil
}

// CloseModule closes a compiled module and releases its resources.
func (m *ModuleLifecycle) CloseModule(ctx context.Context, module wazero.CompiledModule, wasmCID string) error {
	if module == nil {
		return nil
	}

	if err := module.Close(ctx); err != nil {
		m.logger.Warn("Failed to close module",
			zap.String("wasm_cid", wasmCID),
			zap.Error(err),
		)
		return err
	}

	m.logger.Debug("Module closed successfully", zap.String("wasm_cid", wasmCID))
	return nil
}

// CloseModules closes multiple compiled modules.
func (m *ModuleLifecycle) CloseModules(ctx context.Context, modules map[string]wazero.CompiledModule) []error {
	var errors []error

	for cid, module := range modules {
		if err := m.CloseModule(ctx, module, cid); err != nil {
			errors = append(errors, fmt.Errorf("failed to close module %s: %w", cid, err))
		}
	}

	return errors
}

// ValidateModule performs basic validation on compiled module.
func (m *ModuleLifecycle) ValidateModule(module wazero.CompiledModule) error {
	if module == nil {
		return fmt.Errorf("module is nil")
	}
	// Additional validation could be added here
	return nil
}
