package deployments

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/secrets"
)

// EnvEncryptionPurpose is the HKDF info label that separates the deployment
// environment key from every other key derived from the encryption root.
//
// This label is a domain separator, not a rotation handle. Rotating stored
// secrets is `orama maint operator rotate-secrets --rotate`.
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
	key    []byte
	holder *secrets.Holder
}

// SetHolder lets a rotate take effect without restarting this process.
func (c *EnvCodec) SetHolder(h *secrets.Holder) {
	if c != nil {
		c.holder = h
	}
}

// NewEnvCodec derives the environment key from the encryption-root IKM.
//
// It refuses an empty IKM rather than storing plaintext: the caller decides
// what to do without one, and nothing decides to write secrets in the clear.
func NewEnvCodec(ikm string) (*EnvCodec, error) {
	key, err := secrets.DeriveKey(strings.TrimSpace(ikm), EnvEncryptionPurpose)
	if err != nil {
		return nil, fmt.Errorf("failed to derive the deployment environment key: %w", err)
	}
	return &EnvCodec{key: key}, nil
}

// envAAD is what an environment is sealed to: the namespace and the deployment
// id of the row it is stored in. A ciphertext copied to another deployment's row
// (or another namespace's) therefore does not open there. Both are required: an
// environment with no row to be bound to is a bug in the caller.
func envAAD(namespace, deploymentID string) ([]byte, error) {
	aad := secrets.BoundAAD(EnvEncryptionPurpose, namespace, deploymentID)
	if aad == nil {
		return nil, fmt.Errorf("an environment is bound to its namespace and deployment id, got namespace %q and id %q", namespace, deploymentID)
	}
	return aad, nil
}

// Encode returns the stored form of the environment of deployment deploymentID
// in namespace.
//
// Once the operator has enabled bound writes (`orama maint operator rotate-secrets`,
// run when every gateway is on a binary that reads enc:v2:) the stored form is
// bound to the namespace and the deployment id. Before that it is the unbound
// form, which every gateway of the fleet can read during a rolling upgrade.
func (c *EnvCodec) Encode(namespace, deploymentID string, env map[string]string) (string, error) {
	if c == nil {
		return "", fmt.Errorf("no deployment environment codec: refusing to store an environment in the clear")
	}
	aad, err := envAAD(namespace, deploymentID)
	if err != nil {
		return "", err
	}
	if err := ValidateEnv(env); err != nil {
		return "", err
	}
	if err := ValidateEnvSize(env); err != nil {
		return "", err
	}
	if env == nil {
		env = map[string]string{}
	}
	plain, err := json.Marshal(env)
	if err != nil {
		return "", fmt.Errorf("failed to encode the deployment environment: %w", err)
	}
	sealed, err := secrets.SealBound(c.holder, EnvEncryptionPurpose, c.key, aad, string(plain))
	if err != nil {
		return "", fmt.Errorf("failed to encrypt the deployment environment: %w", err)
	}
	return sealed, nil
}

// Decode returns the environment held in stored, the environment of deployment
// deploymentID in namespace.
//
// Three stored forms are read, told apart by their prefix and never by trying
// one after another: plaintext JSON (written before the environment was
// encrypted), the unbound envelopes enc: and enc:v1:<id>: (written before the
// binding existed, or while a rolling upgrade had not finished), and the bound
// enc:v2:<id>: envelope, which opens only for the namespace and deployment id it
// was sealed to. Plaintext and unbound rows are rewritten bound the next time
// the environment is written or the operator runs rotate-secrets, so neither is
// a permanent second format. A row that is none of them is an error: an
// environment that cannot be read is not an empty environment, and starting the
// app without its database URL is worse than not starting it.
func (c *EnvCodec) Decode(namespace, deploymentID, stored string) (map[string]string, error) {
	if c == nil {
		return nil, fmt.Errorf("no deployment environment codec: cannot read a stored environment")
	}
	stored = strings.TrimSpace(stored)
	if stored == "" || stored == "null" {
		return map[string]string{}, nil
	}

	plain := stored
	if secrets.IsEncrypted(stored) {
		aad, err := envAAD(namespace, deploymentID)
		if err != nil {
			return nil, err
		}
		plain, err = secrets.OpenBound(c.holder, EnvEncryptionPurpose, c.key, aad, stored)
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
