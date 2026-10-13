package provision

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"
	"gopkg.in/yaml.v3"
)

// cloudConfigHeader starts every cloud-init user data document.
const cloudConfigHeader = "#cloud-config\n"

// serverHostKey is a host key the harness generates for a server before the
// server exists. Its private half goes to the server once, in the Hetzner
// user data; only the public half is kept.
//
// This replaces trust on first use. What remains assumed: the image runs
// cloud-init's ssh module (cc_ssh) before sshd accepts connections, or at
// least before the confirmation deadline (an sshd presenting any other key is
// never trusted, only waited out); and the user data, which carries the
// private key, is readable by whoever holds the Hetzner project token and by
// any process on the server through the metadata service for the server's
// life. The key protects the run's connections from the network, not from
// the Hetzner project or the server itself.
type serverHostKey struct {
	pub      ssh.PublicKey
	userData string
}

// hostKeyCloudConfig is the part of cloud-config that installs the key.
// ssh_deletekeys false keeps cloud-init from regenerating keys over it.
type hostKeyCloudConfig struct {
	SSHDeleteKeys bool              `yaml:"ssh_deletekeys"`
	SSHKeys       map[string]string `yaml:"ssh_keys"`
}

// newServerHostKey generates an ed25519 host key for the server called name
// and the user data that installs it.
func newServerHostKey(name string) (serverHostKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return serverHostKey{}, fmt.Errorf("failed to generate the host key of %s: %w", name, err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "host "+name)
	if err != nil {
		return serverHostKey{}, fmt.Errorf("failed to encode the host key of %s: %w", name, err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return serverHostKey{}, fmt.Errorf("failed to encode the public host key of %s: %w", name, err)
	}
	doc, err := yaml.Marshal(hostKeyCloudConfig{
		SSHDeleteKeys: false,
		SSHKeys: map[string]string{
			"ed25519_private": string(pem.EncodeToMemory(block)),
			"ed25519_public":  strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))),
		},
	})
	if err != nil {
		return serverHostKey{}, fmt.Errorf("failed to encode the cloud-config of %s: %w", name, err)
	}
	return serverHostKey{pub: sshPub, userData: cloudConfigHeader + string(doc)}, nil
}
