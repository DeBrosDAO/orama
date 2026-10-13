package setup

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"

	"github.com/DeBrosOfficial/network/pkg/clusterreg"
)

const (
	// relayPageLimit is how many of an operator's relays one read asks for; x/relay clamps a page to 100.
	relayPageLimit = 100
	// relayPageMax bounds the pages read for one operator: an operator that runs more relays than
	// this has more than any setup run registers.
	relayPageMax = 50
)

// RelayOfNode reads the operator's relays from x/relay and returns the one registered on nodeID.
func (s *restSession) RelayOfNode(ctx context.Context, operator, nodeID string) (*RegisteredRelay, error) {
	account, err := clusterreg.CanonicalAccount(operator)
	if err != nil {
		return nil, fmt.Errorf("operator: %w", err)
	}
	key := ""
	for page := 0; page < relayPageMax; page++ {
		var doc struct {
			Relays []struct {
				Relay struct {
					NodeID         string `json:"node_id"`
					RSAFingerprint string `json:"rsa_fingerprint"`
					Ed25519ID      string `json:"ed25519_id"`
					Exit           bool   `json:"exit"`
				} `json:"relay"`
			} `json:"relays"`
			Pagination struct {
				NextKey string `json:"next_key"`
			} `json:"pagination"`
		}
		path := fmt.Sprintf("/orama/relay/v1/operator-relays/%s?pagination.limit=%d", url.PathEscape(account), relayPageLimit)
		if key != "" {
			path += "&pagination.key=" + url.QueryEscape(key)
		}
		if found, err := s.get(ctx, path, &doc); err != nil {
			return nil, err
		} else if !found {
			return nil, fmt.Errorf("the chain has no relay query: its x/relay predates OperatorRelays")
		}
		for _, entry := range doc.Relays {
			if entry.Relay.NodeID != nodeID {
				continue
			}
			fingerprint, err := base64.StdEncoding.DecodeString(entry.Relay.RSAFingerprint)
			if err != nil {
				return nil, fmt.Errorf("the chain's relay fingerprint %q is not base64: %w", entry.Relay.RSAFingerprint, err)
			}
			ed25519ID, err := base64.StdEncoding.DecodeString(entry.Relay.Ed25519ID)
			if err != nil {
				return nil, fmt.Errorf("the chain's relay ed25519 id %q is not base64: %w", entry.Relay.Ed25519ID, err)
			}
			return &RegisteredRelay{Fingerprint: fingerprint, Ed25519ID: ed25519ID, Exit: entry.Relay.Exit}, nil
		}
		if key = doc.Pagination.NextKey; key == "" {
			return nil, nil
		}
	}
	return nil, fmt.Errorf("operator %s has more than %d relay pages on the chain", account, relayPageMax)
}
