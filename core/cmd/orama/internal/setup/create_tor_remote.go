package setup

import (
	"bytes"
	"context"
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/tornet"
)

var _ TorHost = (*sshMachine)(nil)

// AuthorityKeys makes the authority's keys on the machine, or reads back the ones it has.
func (m *sshMachine) AuthorityKeys(ctx context.Context, in AuthorityKeysInput) (tornet.AuthorityKeys, error) {
	out, err := m.capture(ctx, authorityKeysCommand(in), nil)
	if err != nil {
		return tornet.AuthorityKeys{}, err
	}
	keys, err := ParseAuthorityKeys(out)
	if err != nil {
		return tornet.AuthorityKeys{}, fmt.Errorf("machine %s printed an authority keys document that cannot be used: %w", m.node.Host, err)
	}
	return keys, nil
}

// InstallTor puts the network file beside the staged release and installs the role.
func (m *sshMachine) InstallTor(ctx context.Context, in TorInstall) error {
	if err := m.sh.Run(ctx, writeTorNetworkCommand(), bytes.NewReader(in.Network), nil); err != nil {
		return fmt.Errorf("put the Tor network file on the machine: %w", err)
	}
	if err := m.stream(ctx, TorInstallCommand(in), nil); err != nil {
		return fmt.Errorf("orama global install --services %s: %w", torServiceOf(in.Role), err)
	}
	return nil
}

// StartTor starts the role's units.
func (m *sshMachine) StartTor(ctx context.Context, role TorRole) error {
	if err := m.stream(ctx, torLifecycleCommand("start", role), nil); err != nil {
		return fmt.Errorf("start the %s: %w", role, err)
	}
	return nil
}

// RestartTor restarts the role. The machine's own refusal, for an authority that
// may not be restarted yet, is part of the error.
func (m *sshMachine) RestartTor(ctx context.Context, role TorRole) error {
	if err := m.stream(ctx, torLifecycleCommand("restart", role), nil); err != nil {
		return fmt.Errorf("restart the %s: %w", role, err)
	}
	return nil
}

// TorState reads what the machine has of the role.
func (m *sshMachine) TorState(ctx context.Context, role TorRole) (TorState, error) {
	out, err := m.capture(ctx, bash(m.sudo(), torStateScript(role)), nil)
	if err != nil {
		return TorState{}, err
	}
	return ParseTorState(out, role)
}
