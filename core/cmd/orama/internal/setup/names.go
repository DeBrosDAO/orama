package setup

import (
	"context"
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/nodenames"
)

// ChainNames claims names on the chain the operator's session is open on.
type ChainNames struct{}

// Claim claims name for nodeID. The name is checked against the chain's grammar and reserved
// list first, so a name the chain would refuse costs no fee. A node that already holds the name
// is done; a node that holds another one is an error, since the chain gives a node a single name
// and releasing one is the operator's decision, not setup's.
func (ChainNames) Claim(ctx context.Context, chain NameChain, name, nodeID string) error {
	if err := nodenames.ValidateName(name); err != nil {
		return fmt.Errorf("the name %q cannot be claimed: %w (choose another --name)", name, err)
	}
	held, err := chain.NodeName(ctx, nodeID)
	if err != nil {
		return fmt.Errorf("check whether node %q holds a name: %w", nodeID, err)
	}
	switch {
	case held == name:
		return nil
	case held != "":
		return fmt.Errorf("node %q already holds the name %q on the chain, and a node holds one name: release it first, or run setup with --name %s", nodeID, held, held)
	}
	if _, err := chain.ClaimNodeName(ctx, nodeID, name); err != nil {
		return fmt.Errorf("claim the name %q for node %q (a name belongs to the first operator who claims it): %w", name, nodeID, err)
	}
	return nil
}
