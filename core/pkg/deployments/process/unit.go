package process

import (
	"fmt"
	"sort"
)

// What the platform puts in a deployment's environment.
//
// The unit itself is a template installed with the release (`systemd/
// orama-deploy-*@.service`), not a file this package writes: the gateway used
// to `tee` one into /etc, which only worked because it ran as root, and the
// hardened gateway unit takes that away. Everything that varies per deployment
// is either derived from the systemd instance or read from here.

const (
	// stateDirectoryRoot is where systemd creates each deployment's writable
	// directory, under /var/lib. The app's own directory is read-only: the
	// files there are its build output, and a process that can rewrite its own
	// code cannot be rolled back to a known version.
	stateDirectoryRoot = "/var/lib"
	// cacheDirectoryRoot mirrors stateDirectoryRoot, under /var/cache.
	cacheDirectoryRoot = "/var/cache"
)

// StateDirectoryPath is the writable directory systemd creates for a
// deployment, exported to it as ORAMA_STATE_DIR.
func StateDirectoryPath(serviceName string) string {
	return stateDirectoryRoot + "/" + serviceName
}

// CacheDirectoryPath is the deployment's cache directory, exported to it as
// ORAMA_CACHE_DIR.
func CacheDirectoryPath(serviceName string) string {
	return cacheDirectoryRoot + "/" + serviceName
}

// PlatformEnvKeys are the variables the platform sets in every deployment. A
// tenant cannot set them: they are how the app knows which namespace it belongs
// to and where it may write, and a deployment that could overwrite them could
// point itself at another namespace's gateway.
var PlatformEnvKeys = []string{
	"PORT",
	"ORAMA_NAMESPACE",
	"ORAMA_GATEWAY_URL",
	"ORAMA_STATE_DIR",
	"ORAMA_CACHE_DIR",
	entryPointEnvKey,
}

// platformEnv returns the variables the platform sets for one deployment.
func platformEnv(namespace, serviceName, gatewayURL, entryPoint string, port int) map[string]string {
	env := map[string]string{
		"PORT":            fmt.Sprintf("%d", port),
		"ORAMA_NAMESPACE": namespace,
		"ORAMA_STATE_DIR": StateDirectoryPath(serviceName),
		"ORAMA_CACHE_DIR": CacheDirectoryPath(serviceName),
	}
	if gatewayURL != "" {
		env["ORAMA_GATEWAY_URL"] = gatewayURL
	}
	// The node template runs `node ${ORAMA_ENTRYPOINT}`. systemd expands a
	// variable in an argument but not in the executable, which is why the
	// script is here and the interpreter is in the template.
	if entryPoint != "" {
		env[entryPointEnvKey] = entryPoint
	}
	return env
}

// mergeEnv returns the deployment's environment with the platform's variables
// applied last, so a tenant value can never displace one of them.
func mergeEnv(tenant map[string]string, platform map[string]string) map[string]string {
	merged := make(map[string]string, len(tenant)+len(platform))
	for key, value := range tenant {
		merged[key] = value
	}
	for key, value := range platform {
		merged[key] = value
	}
	return merged
}

// sortedEnv returns "KEY=value" strings in a stable order, for the direct
// (non-systemd) runner.
func sortedEnv(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+env[key])
	}
	return out
}
