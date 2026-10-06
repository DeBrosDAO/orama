package install

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
)

// A remote install runs `sudo orama node install ...` on the new machine over
// SSH. The invite token and, on the manual join path, the cluster secret and
// the swarm key used to be arguments on that command line, where every local
// user of the new machine could read them in ps for as long as the install ran.
// They travel on the command's stdin instead, as one JSON object, and the
// command line carries only --secrets-stdin.
//
// stdin rather than the other ways to hand a process data: an environment
// variable needs sshd's AcceptEnv and is dropped by sudo; a file copied ahead
// lands on the new machine's disk; stdin is a pipe that exists only while the
// command reads it.

// secretsStdinFlag is the node-side flag that says the secrets are on stdin.
const secretsStdinFlag = "secrets-stdin"

// maxStdinSecretsBytes bounds the read: the object holds three short strings.
const maxStdinSecretsBytes = 64 << 10

// stdinSecrets is the object a remote install writes to the node's stdin.
type stdinSecrets struct {
	Token         string `json:"token,omitempty"`
	ClusterSecret string `json:"cluster_secret,omitempty"`
	SwarmKey      string `json:"swarm_key,omitempty"`
}

// remoteSecrets is what a remote install sends on stdin, or nil when the
// operator gave none of the secrets.
func remoteSecrets(flags *Flags) ([]byte, error) {
	s := stdinSecrets{Token: flags.Token, ClusterSecret: flags.ClusterSecret, SwarmKey: flags.SwarmKey}
	if s == (stdinSecrets{}) {
		return nil, nil
	}
	payload, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("encode the install secrets for the node's stdin: %w", err)
	}
	return payload, nil
}

// readStdinSecrets fills the secret flags from r when --secrets-stdin is set.
//
// A secret also given on the command line is refused rather than overridden:
// the two sources disagreeing means something built the command wrongly.
func (f *Flags) readStdinSecrets(r io.Reader) error {
	if !f.SecretsFromStdin {
		return nil
	}
	if f.Remote {
		return clierr.Usage("--%s is for the node end of a remote install; it cannot be combined with --remote", secretsStdinFlag)
	}
	if f.Token != "" || f.ClusterSecret != "" || f.SwarmKey != "" {
		return clierr.Usage("--%s was given together with --token, --cluster-secret or --swarm-key; pass the secrets one way", secretsStdinFlag)
	}

	data, err := io.ReadAll(io.LimitReader(r, maxStdinSecretsBytes+1))
	if err != nil {
		return fmt.Errorf("read the install secrets from stdin: %w", err)
	}
	if len(data) > maxStdinSecretsBytes {
		return clierr.Usage("--%s: stdin holds more than %d bytes; it should be one small JSON object", secretsStdinFlag, maxStdinSecretsBytes)
	}

	var s stdinSecrets
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return clierr.Usage("--%s: stdin is not the install secrets object: %v", secretsStdinFlag, err)
	}
	if dec.More() {
		return clierr.Usage("--%s: stdin holds more than the one install secrets object", secretsStdinFlag)
	}
	if s == (stdinSecrets{}) {
		return clierr.Usage("--%s: stdin names no secret", secretsStdinFlag)
	}
	f.Token, f.ClusterSecret, f.SwarmKey = s.Token, s.ClusterSecret, s.SwarmKey
	return nil
}
