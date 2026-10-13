package setup

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/setup"
	"github.com/DeBrosOfficial/network/pkg/rwagent"
)

// agentWait bounds a request to the RootWallet agent that may prompt its owner.
const agentWait = 30 * time.Second

// agentWallet is the operator's RootWallet, through its local agent.
type agentWallet struct{ client *rwagent.Client }

func newAgentWallet() agentWallet {
	return agentWallet{client: rwagent.New(os.Getenv("RW_AGENT_SOCK"))}
}

// Unlocked fails with what to do when the agent is not there or is locked: every
// step that follows signs something.
func (w agentWallet) Unlocked(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, agentWait)
	defer cancel()
	status, err := w.client.Status(ctx)
	if err != nil {
		return fmt.Errorf("the RootWallet agent is not reachable: %w (open the RootWallet app, or install it from orama.network/get-started)", err)
	}
	if status.Locked {
		return fmt.Errorf("the RootWallet agent is locked: unlock it in the RootWallet app, then run orama setup again")
	}
	return nil
}

// EVMAddress is the account the cluster's archive trust anchors on.
func (w agentWallet) EVMAddress(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, agentWait)
	defer cancel()
	return setup.OperatorWallet(ctx, w.client)
}

// OramaAddress is the operator account on the chain.
func (w agentWallet) OramaAddress(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, agentWait)
	defer cancel()
	acct, err := w.client.OramaAccount(ctx)
	if err != nil {
		return "", fmt.Errorf("read the RootWallet's orama account: %w", err)
	}
	return acct.Address, nil
}
