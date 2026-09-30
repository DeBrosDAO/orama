package stages

import (
	"net/url"
	"slices"
	"strings"

	"github.com/DeBrosOfficial/network/e2e/harness/secrets"
)

// featureEnvNames are the only variables of the runner's environment a
// feature package inherits by name: what the go command needs to build and
// run the tests, and the proxies. HOME is not among them (the runner hands
// feature processes an empty HOME of the run's own; the go command finds
// its caches through GOCACHE, GOMODCACHE and GOPATH, which the runner
// resolves and passes explicitly), and neither is any cloud credential.
var featureEnvNames = []string{
	"PATH", "LANG", "LC_ALL", "TERM", "TMPDIR", "TZ",
	"GOCACHE", "GOMODCACHE", "GOPATH", "GOTOOLCHAIN",
}

// proxyEnvNames are passed with any user:password stripped from the URL.
var proxyEnvNames = []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy"}

// goSourceEnvNames change where the go command fetches modules from, or
// whether it verifies them; they reach a feature package only with
// E2E_ALLOW_GO_ENV=1. Without them it uses the default proxy and checksum
// database, and the module cache the runner resolved.
var goSourceEnvNames = []string{"GOFLAGS", "GOPROXY", "GOSUMDB", "GONOSUMDB", "GOPRIVATE", "GONOPROXY", "GOINSECURE"}

// EnvAllowGoEnv lets goSourceEnvNames through (1).
const EnvAllowGoEnv = "E2E_ALLOW_GO_ENV"

// featureE2ENames are the run's own settings a feature package reads, by
// name (from a grep of every os.Getenv and os.LookupEnv under e2e/): no
// E2E_* wildcard, so a variable added to the runner's environment for
// something else never reaches a test by accident.
var featureE2ENames = []string{
	"E2E_STRICT", "E2E_FLEET_STATE", "E2E_EVIDENCE_DIR", "E2E_BROKER_SOCK",
	"E2E_PACE_CRED_PER_MIN", "E2E_PACE_CRED_BURST", "E2E_PACE_CHALLENGE_PER_MIN", "E2E_PACE_CHALLENGE_BURST",
	"E2E_MAX_LIVE_NAMESPACES", "E2E_REPO_ROOT", "E2E_SOAK_MINUTES", "E2E_PERF_REGRESSION_PCT",
	"E2E_BASELINE_FILE", "E2E_INSTALL_PREVIOUS", "E2E_ORAMA_TX_SIGNING", "E2E_ORAMAOS_IMAGE", "E2E_ORAMAOS_OVMF",
}

// FeatureEnv filters environ (KEY=VALUE pairs) down to what a feature
// package may see: the names above, never a secret variable
// (secrets.IsSecretEnv: HCLOUD_TOKEN, CF_API_TOKEN, INFISICAL_*, ...). Cloud
// operations a feature needs go through the runner's broker
// (E2E_BROKER_SOCK), which holds the credentials instead.
func FeatureEnv(environ []string) []string {
	allowGo := slices.Contains(environ, EnvAllowGoEnv+"=1")
	var out []string
	for _, kv := range environ {
		name, value, ok := strings.Cut(kv, "=")
		if !ok || name == "" || secrets.IsSecretEnv(name) {
			continue
		}
		switch {
		case slices.Contains(featureEnvNames, name), slices.Contains(featureE2ENames, name),
			allowGo && slices.Contains(goSourceEnvNames, name):
			out = append(out, kv)
		case slices.Contains(proxyEnvNames, name):
			if v, ok := withoutUserinfo(value); ok {
				out = append(out, name+"="+v)
			}
		}
	}
	return out
}

// withoutUserinfo removes user:password from each URL of a proxy setting
// (NO_PROXY is a host list and passes as it is). A value that does not
// parse is dropped rather than passed with a credential in it.
func withoutUserinfo(value string) (string, bool) {
	if !strings.Contains(value, "@") {
		return value, true
	}
	u, err := url.Parse(value)
	if err != nil || u.Host == "" {
		return "", false
	}
	u.User = nil
	return u.String(), true
}
