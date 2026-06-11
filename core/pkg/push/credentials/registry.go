package credentials

import "sync"

// registry is the package-level map of provider name → Validator.
//
// Provider packages (pkg/push/providers/apns, .../ntfy, .../fcm, …)
// export a Validator implementation; the gateway dependency wiring
// calls Register at startup for each provider it wants to support on
// this gateway. Anyone-can-register-anything is intentional — operators
// who want to disable a provider simply don't register its Validator,
// and PUT/GET for that provider return 400 ErrUnknownProvider.
//
// Safe for concurrent reads; mutations should happen at gateway
// startup before request serving begins.
var (
	registryMu sync.RWMutex
	registry   = map[string]Validator{}
)

// Register makes a Validator available for the provider name. Calling
// Register with the same name twice replaces the previous one — useful
// in tests; in production it indicates a wiring bug and is logged by
// the gateway startup path.
//
// Panics if v is nil or v.Provider() is empty: these are programmer
// errors that should fail loud at gateway startup, not mysteriously at
// first PUT.
func Register(v Validator) {
	if v == nil {
		panic("credentials: Register called with nil Validator")
	}
	name := v.Provider()
	if name == "" {
		panic("credentials: Validator.Provider() returned empty string")
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[name] = v
}

// LookupValidator returns the Validator for provider, or (nil, false)
// if no Validator is registered. Used by the PUT/GET handlers to
// reject unknown providers with a 400 + clear error.
func LookupValidator(provider string) (Validator, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	v, ok := registry[provider]
	return v, ok
}

// RegisteredProviders returns the names of all currently-registered
// providers. Used by the "what providers does this gateway support"
// summary endpoint and by tests. Order is unspecified.
func RegisteredProviders() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]string, 0, len(registry))
	for name := range registry {
		out = append(out, name)
	}
	return out
}

// resetRegistry clears the registry. Used internally by the package's
// own tests; the exported ResetRegistryForTest wrapper makes it
// callable from tests in OTHER packages (which can't reach
// package-internal symbols).
//
// Not safe to call while requests are in flight; intended for test
// setup/teardown ONLY.
func resetRegistry() {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry = map[string]Validator{}
}

// ResetRegistryForTest clears the global Validator registry. Tests in
// other packages (e.g. the HTTP handler tests) that register
// Validators should defer this so they don't leak state into other
// tests in the same binary.
//
// Exposed as a regular exported function (not _test.go-gated) because
// test files in other packages cannot reach _test.go-only exports of
// THIS package. Safe to call at runtime but pointless outside tests.
func ResetRegistryForTest() {
	resetRegistry()
}
