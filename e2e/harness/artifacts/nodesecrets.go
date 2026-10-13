package artifacts

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/secrets"
)

// nodeSecretsDir holds a node's own secrets (core/pkg/install writes them).
const nodeSecretsDir = "/opt/orama/.orama/secrets"

// nodeSecretFiles are the secrets read, once per node, before anything is
// collected from any node: their values are registered with the redactor,
// so a cluster secret, the RQLite password or a key that a journal or a
// report prints is masked wherever it appears.
var nodeSecretFiles = []string{
	"cluster-secret", "rqlite-password", "rqlite-auth.json", "secrets-encryption-key",
	"turn-secret", "api-key-hmac-secret", "swarm.key",
}

// nodeSecretMaxBytes bounds one secret file read.
const nodeSecretMaxBytes = 65536

// secretsScript prints each secret file that exists, bounded, one after the
// other. It never fails for a missing file.
func secretsScript() string {
	var b strings.Builder
	b.WriteString("for f in")
	for _, f := range nodeSecretFiles {
		b.WriteString(" " + fleet.ShellQuote(path.Join(nodeSecretsDir, f)))
	}
	fmt.Fprintf(&b, "; do [ -f \"$f\" ] && head -c %d \"$f\" && echo; done; true", nodeSecretMaxBytes)
	return b.String()
}

// registerNodeSecrets reads n's secrets and adds their values to red.
func registerNodeSecrets(ctx context.Context, sh fleet.Shell, red *secrets.Redactor) error {
	it := runItem(ctx, sh, "", secretsScript())
	if it.err != nil {
		return fmt.Errorf("failed to read the node's secrets for redaction: %w", it.err)
	}
	if err := red.Add(secretValues(it.out)...); err != nil {
		return fmt.Errorf("failed to register the node's secrets for redaction: %w", err)
	}
	return nil
}

// secretValues are the values in secret file text: each line, and each
// string value of a line that is a JSON object (rqlite-auth.json).
func secretValues(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out = append(out, line)
		out = append(out, jsonStrings(line)...)
	}
	return out
}

// jsonStrings are the string values anywhere in a JSON document; none when
// text is not JSON.
func jsonStrings(text string) []string {
	var v any
	if json.Unmarshal([]byte(text), &v) != nil {
		return nil
	}
	var out []string
	var walk func(any)
	walk = func(x any) {
		switch t := x.(type) {
		case string:
			out = append(out, t)
		case []any:
			for _, e := range t {
				walk(e)
			}
		case map[string]any:
			for _, e := range t {
				walk(e)
			}
		}
	}
	walk(v)
	return out
}
