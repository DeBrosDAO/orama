package setup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/clusterops"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/dnsdelegation"
	"github.com/DeBrosOfficial/network/pkg/constants"
)

// InstallGlobal puts the genesis (and the Tor network file) on the machine,
// runs `orama global install` and starts the services that need no registration.
func (m *sshMachine) InstallGlobal(ctx context.Context, in GlobalInstall) (err error) {
	if err := m.sh.Run(ctx, stageGlobalDirCommand(), nil, nil); err != nil {
		return fmt.Errorf("make the directory for the genesis: %w", err)
	}
	defer func() {
		if rmErr := m.sh.Run(context.WithoutCancel(ctx), removeGlobalStageCommand(), nil, nil); rmErr != nil {
			err = errors.Join(err, fmt.Errorf("remove %s: %w", globalStageDir, rmErr))
		}
	}()
	if err := m.sh.Run(ctx, writeGenesisCommand(), bytes.NewReader(in.Genesis), nil); err != nil {
		return fmt.Errorf("put the genesis on the machine: %w", err)
	}
	if len(in.TorNetwork) > 0 {
		if err := m.sh.Run(ctx, writeTorNetworkCommand(), bytes.NewReader(in.TorNetwork), nil); err != nil {
			return fmt.Errorf("put the Tor network file on the machine: %w", err)
		}
	}
	if err := m.stream(ctx, GlobalInstallCommand(in), nil); err != nil {
		return fmt.Errorf("orama global install: %w", err)
	}
	return m.StartGlobal(ctx, in.Node)
}

// StartGlobal starts the chain, the public IPFS and the relay.
func (m *sshMachine) StartGlobal(ctx context.Context, node NodePlan) error {
	if err := m.stream(ctx, StartGlobalCommand(node), nil); err != nil {
		return fmt.Errorf("start the global services: %w", err)
	}
	return nil
}

// ChainState is one poll of the chain node.
func (m *sshMachine) ChainState(ctx context.Context) (ChainState, error) {
	out, err := m.capture(ctx, bash(m.sudo(), chainStateScript()), nil)
	if err != nil {
		return ChainState{}, err
	}
	return ParseChainState(out)
}

// Identity reads the node's chain keys and signs its hot-key binding.
func (m *sshMachine) Identity(ctx context.Context, in IdentityRequest) (NodeIdentity, error) {
	out, err := m.capture(ctx, bash(m.sudo(), identityScript(in)), nil)
	if err != nil {
		return NodeIdentity{}, err
	}
	return ParseIdentity(out)
}

// StartServices writes the node id the provider reads and starts it.
func (m *sshMachine) StartServices(ctx context.Context, nodeID string) error {
	return m.stream(ctx, bash(m.sudo(), startServicesScript()), strings.NewReader(nodeID))
}

// Nameservers reads the cluster's nameserver slots on this machine, over its
// pinned SSH session: what is typed into a registrar comes from a machine setup
// has the host key of.
func (m *sshMachine) Nameservers(_ context.Context) ([]dnsdelegation.Delegation, error) {
	body, err := m.query(m.node, dnsdelegation.Query)
	if err != nil {
		return nil, fmt.Errorf("read the nameserver slots on %s: %w", m.node.Host, err)
	}
	rows, err := clusterops.Rows(body)
	if err != nil {
		return nil, fmt.Errorf("read the nameserver slots on %s: %w", m.node.Host, err)
	}
	return dnsdelegation.FromRows(rows)
}

// OpenChain forwards a local port to the chain's REST API in the node's
// namespace and returns its URL.
func (m *sshMachine) OpenChain(ctx context.Context) (string, func(), error) {
	local, stop, err := m.startTunnel(ctx, m.node, fmt.Sprintf("%s:%d", constants.GlobalNetnsAddr, constants.ChainAPIPort))
	if err != nil {
		return "", nil, err
	}
	return "http://" + local, stop, nil
}
