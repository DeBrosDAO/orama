package enroll

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/secrets"
)

// agentTokenPurpose is the HKDF domain separator for encrypting node agent
// tokens at rest, so the key is unrelated to the TURN, push and enrollment keys
// derived from the same cluster secret.
const agentTokenPurpose = "node-agent-token"

// storeAgentToken records the credential a node minted for this gateway.
//
// The gateway has to be able to present it later, so it cannot be hashed — it
// is encrypted with a key derived from the cluster secret, the same treatment
// the TURN shared secrets get. A registry snapshot yields a blob rather than
// the ability to restart every service on every OramaOS node.
func (h *Handler) storeAgentToken(ctx context.Context, nodeID, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return fmt.Errorf("refusing to store an empty agent token for %s", nodeID)
	}

	key, err := h.agentTokenKey()
	if err != nil {
		return err
	}
	encrypted, err := secrets.Encrypt(token, key)
	if err != nil {
		return fmt.Errorf("could not encrypt the agent token for %s: %w", nodeID, err)
	}

	if _, err := h.rqliteClient.Exec(ctx,
		"UPDATE wireguard_peers SET agent_token = ? WHERE node_id = ?", encrypted, nodeID); err != nil {
		return fmt.Errorf("could not store the agent token for %s: %w", nodeID, err)
	}
	return nil
}

// AgentToken returns the credential to present to a node's agent.
//
// An empty result is not "no authentication needed": it means this gateway
// cannot command that node, and the caller must say so rather than sending an
// unauthenticated request the agent will refuse anyway.
func (h *Handler) AgentToken(ctx context.Context, nodeID string) (string, error) {
	var rows []struct {
		Token string `db:"agent_token"`
	}
	if err := h.rqliteClient.Query(ctx, &rows,
		"SELECT COALESCE(agent_token, '') AS agent_token FROM wireguard_peers WHERE node_id = ? LIMIT 1",
		nodeID); err != nil {
		return "", fmt.Errorf("could not read the agent token for %s: %w", nodeID, err)
	}
	if len(rows) == 0 || strings.TrimSpace(rows[0].Token) == "" {
		return "", fmt.Errorf("no agent token for %s: it enrolled before agent tokens existed, "+
			"or its enrollment did not complete; re-enrol it to command it", nodeID)
	}

	key, err := h.agentTokenKey()
	if err != nil {
		return "", err
	}
	token, err := secrets.Decrypt(rows[0].Token, key)
	if err != nil {
		return "", fmt.Errorf("the stored agent token for %s did not decrypt: %w", nodeID, err)
	}
	return token, nil
}

// agentTokenKey derives the encryption key from the cluster secret on disk.
func (h *Handler) agentTokenKey() ([]byte, error) {
	raw, err := os.ReadFile(h.oramaDir + "/secrets/cluster-secret")
	if err != nil {
		return nil, fmt.Errorf("could not read the cluster secret, so agent tokens cannot be "+
			"encrypted or read: %w", err)
	}
	return secrets.DeriveKey(strings.TrimSpace(string(raw)), agentTokenPurpose)
}
