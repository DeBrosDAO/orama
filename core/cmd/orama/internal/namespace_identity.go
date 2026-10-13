package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/libp2p/go-libp2p/core/peer"
	"gopkg.in/yaml.v3"

	oramainstall "github.com/DeBrosOfficial/network/pkg/install"
)

// localNodePeerID is the libp2p peer id of the node this command runs on, from
// the node.id in node.yaml, which install writes as the public id of the
// identity key. A coordination stamp is signed for it: the request goes to this
// node's own gateway, which accepts only a stamp made for its own id.
//
// The identity key itself is never opened: it is the node's private key, and
// this command only needs the public name it has.
func localNodePeerID() (string, error) {
	path := filepath.Join(oramainstall.OramaConfigs, "node.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("cannot read this node's configuration at %s, so the request cannot be "+
			"signed for this node's gateway — run this on a node: %w", path, err)
	}
	return peerIDFromNodeConfig(data, path)
}

// peerIDFromNodeConfig reads node.id from node.yaml and requires it to be a
// libp2p peer id. Nodes installed before node.id was the peer id carry the
// first label of their domain there; that is refused with the way out, rather
// than signing for a name the gateway would never recognise.
func peerIDFromNodeConfig(data []byte, path string) (string, error) {
	var cfg struct {
		Node struct {
			ID string `yaml:"id"`
		} `yaml:"node"`
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return "", fmt.Errorf("%s does not parse: %w", path, err)
	}
	if cfg.Node.ID == "" {
		return "", fmt.Errorf("%s has no node.id, so this node's peer id is unknown; "+
			"run `orama node upgrade` to rewrite it", path)
	}
	if _, err := peer.Decode(cfg.Node.ID); err != nil {
		return "", fmt.Errorf("node.id %q in %s is not a libp2p peer id (this node was installed before "+
			"node.id was the peer id); run `orama node upgrade` to rewrite it: %w", cfg.Node.ID, path, err)
	}
	return cfg.Node.ID, nil
}
