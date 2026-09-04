package deployments

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/secrets"
)

// EnvEncryptionPurpose is the HKDF info label that separates the deployment
// environment key from every other key derived from the cluster secret.
//
// Changing it is a deliberate rotation that makes every stored environment
// unreadable, so it must never be edited casually.
const EnvEncryptionPurpose = "orama-deployment-environment-v1"

// EnvCodec turns a deployment's environment into the single column it is stored
// in, and back.
//
// The column held plaintext JSON. The platform's own guide tells people to put
// their secrets in environment variables, so that column is where a namespace's
// API keys and database passwords live — replicated by Raft to every node in
// the cluster and present in every backup of it. It is encrypted now, with a
// key derived from the cluster secret, so a node's database file is not a list
// of every tenant's credentials.
type EnvCodec struct {
	key []byte
}

// NewEnvCodec derives the environment key from the cluster secret.
//
// It refuses an empty secret rather than storing plaintext: the caller decides
// what to do without one, and nothing decides to write secrets in the clear.
func NewEnvCodec(clusterSecret string) (*EnvCodec, error) {
	key, err := secrets.DeriveKey(strings.TrimSpace(clusterSecret), EnvEncryptionPurpose)
	if err != nil {
		return nil, fmt.Errorf("failed to derive the deployment environment key: %w", err)
	}
	return &EnvCodec{key: key}, nil
}

// Encode returns the stored form of env.
func (c *EnvCodec) Encode(env map[string]string) (string, error) {
	if c == nil {
		return "", fmt.Errorf("no deployment environment codec: refusing to store an environment in the clear")
	}
	if err := ValidateEnv(env); err != nil {
		return "", err
	}
	if env == nil {
		env = map[string]string{}
	}
	plain, err := json.Marshal(env)
	if err != nil {
		return "", fmt.Errorf("failed to encode the deployment environment: %w", err)
	}
	sealed, err := secrets.Encrypt(string(plain), c.key)
	if err != nil {
		return "", fmt.Errorf("failed to encrypt the deployment environment: %w", err)
	}
	return sealed, nil
}

// Decode returns the environment held in stored.
//
// A row written before the environment was encrypted holds plaintext JSON.
// Those rows are read as they are and rewritten encrypted the next time the
// deployment's environment is written, so the plaintext is not a permanent
// second format. A row that is neither is an error: an environment that cannot
// be read is not an empty environment, and starting the app without its
// database URL is worse than not starting it.
func (c *EnvCodec) Decode(stored string) (map[string]string, error) {
	if c == nil {
		return nil, fmt.Errorf("no deployment environment codec: cannot read a stored environment")
	}
	stored = strings.TrimSpace(stored)
	if stored == "" || stored == "null" {
		return map[string]string{}, nil
	}

	plain := stored
	if secrets.IsEncrypted(stored) {
		var err error
		plain, err = secrets.Decrypt(stored, c.key)
		if err != nil {
			return nil, fmt.Errorf("failed to decrypt the deployment environment: %w", err)
		}
	}

	env := map[string]string{}
	if err := json.Unmarshal([]byte(plain), &env); err != nil {
		return nil, fmt.Errorf("failed to decode the deployment environment: %w", err)
	}
	if env == nil {
		env = map[string]string{}
	}
	return env, nil
}

// IsEncrypted reports whether a stored environment is already sealed. It is how
// a caller tells a legacy plaintext row from a current one.
func (c *EnvCodec) IsEncrypted(stored string) bool {
	return secrets.IsEncrypted(strings.TrimSpace(stored))
}
