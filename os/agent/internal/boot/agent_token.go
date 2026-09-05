package boot

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AgentTokenPath is where this node's own credential lives.
//
// It sits on the encrypted data partition, not the read-only rootfs, because
// it is per-node state minted at enrollment. 0600 and owned by root: the agent
// is the only process on an OramaOS node, so nothing else should be able to
// read it, and there is no shell to read it with.
const AgentTokenPath = "/opt/orama/.orama/secrets/agent-token"

// storeAgentToken writes the credential the gateway must present on every
// command to this node.
//
// It is written once, at enrollment. Without it the command receiver refuses to
// start — which is the correct outcome for a node that cannot authenticate the
// gateway, rather than one that listens on every interface and authenticates
// nobody.
func (a *Agent) storeAgentToken(token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return fmt.Errorf("refusing to store an empty agent token")
	}
	if err := os.MkdirAll(filepath.Dir(AgentTokenPath), 0700); err != nil {
		return fmt.Errorf("failed to create the secrets directory: %w", err)
	}
	if err := os.WriteFile(AgentTokenPath, []byte(token), 0600); err != nil {
		return fmt.Errorf("failed to write %s: %w", AgentTokenPath, err)
	}
	a.agentToken = token
	return nil
}

// loadAgentToken reads the credential written at enrollment.
//
// A node enrolled before agent tokens existed has no file. That is not an error
// to recover from by starting an unauthenticated receiver: the node keeps
// running its services and simply cannot be commanded until it is re-enrolled.
func (a *Agent) loadAgentToken() error {
	raw, err := os.ReadFile(AgentTokenPath)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", AgentTokenPath, err)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return fmt.Errorf("%s is empty", AgentTokenPath)
	}
	a.agentToken = token
	return nil
}
