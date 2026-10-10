package setup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
)

// maxGenesisBytes bounds a genesis read back from a machine: the installer
// accepts 64 MiB (pkg/install genesisLimit), and a genesis with the standard
// contracts is a few MiB.
const maxGenesisBytes = 64 << 20

// limited collects a command's output up to max bytes; past it a write fails,
// which ends the command.
type limited struct {
	bytes.Buffer
	max int
}

func (l *limited) Write(p []byte) (int, error) {
	if l.Len()+len(p) > l.max {
		return 0, fmt.Errorf("the command printed more than %d bytes", l.max)
	}
	return l.Buffer.Write(p)
}

var _ Bootstrapper = (*sshMachine)(nil)

// InitChain puts a placeholder genesis (and the Tor network file) on the machine
// and runs the first `orama global install`: the chain home and the node's keys.
func (m *sshMachine) InitChain(ctx context.Context, in InitChainInput) (err error) {
	if err := m.sh.Run(ctx, stageGlobalDirCommand(), nil, nil); err != nil {
		return fmt.Errorf("make the directory for the genesis: %w", err)
	}
	defer func() {
		if rmErr := m.sh.Run(context.WithoutCancel(ctx), removeGlobalStageCommand(), nil, nil); rmErr != nil {
			err = errors.Join(err, fmt.Errorf("remove %s: %w", globalStageDir, rmErr))
		}
	}()
	if err := m.sh.Run(ctx, writeGenesisCommand(), bytes.NewReader(placeholderGenesis(in.ChainID)), nil); err != nil {
		return fmt.Errorf("put the placeholder genesis on the machine: %w", err)
	}
	if len(in.TorNetwork) > 0 {
		if err := m.sh.Run(ctx, writeTorNetworkCommand(), bytes.NewReader(in.TorNetwork), nil); err != nil {
			return fmt.Errorf("put the Tor network file on the machine: %w", err)
		}
	}
	if err := m.stream(ctx, InitChainCommand(in), nil); err != nil {
		return fmt.Errorf("orama global install --init-chain: %w", err)
	}
	return nil
}

// Seat reads the node's keys and the seat's account.
func (m *sshMachine) Seat(ctx context.Context) (Seat, error) {
	out, err := m.capture(ctx, bash(m.sudo(), seatScript()), nil)
	if err != nil {
		return Seat{}, err
	}
	return ParseSeat(out)
}

// HomeState says what the chain home holds.
func (m *sshMachine) HomeState(ctx context.Context) (HomeState, error) {
	out, err := m.capture(ctx, bash(m.sudo(), homeStateScript()), nil)
	if err != nil {
		return HomeState{}, err
	}
	return ParseHomeState(out)
}

// BuildGenesis builds the genesis in a scratch home on the machine.
func (m *sshMachine) BuildGenesis(ctx context.Context, steps [][]string) ([]byte, error) {
	return m.captureGenesis(ctx, bash(m.sudo(), genesisScript(steps)))
}

// ReadGenesis is the genesis in the chain home.
func (m *sshMachine) ReadGenesis(ctx context.Context) ([]byte, error) {
	return m.captureGenesis(ctx, readGenesisCommand())
}

func (m *sshMachine) captureGenesis(ctx context.Context, command string) ([]byte, error) {
	out := &limited{max: maxGenesisBytes}
	if err := m.sh.Run(ctx, command, nil, out); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// PutGenesis replaces the chain home's genesis.
func (m *sshMachine) PutGenesis(ctx context.Context, genesis []byte) error {
	return m.sh.Run(ctx, putGenesisCommand(), bytes.NewReader(genesis), nil)
}

// WireChain runs `orama global install` again with the persistent peers.
func (m *sshMachine) WireChain(ctx context.Context, in WireInput) error {
	if err := m.stream(ctx, WireChainCommand(in), nil); err != nil {
		return fmt.Errorf("orama global install --persistent-peers: %w", err)
	}
	return nil
}

// ChainHealth is one poll of the chain.
func (m *sshMachine) ChainHealth(ctx context.Context) (ChainHealth, error) {
	out, err := m.capture(ctx, bash(m.sudo(), chainStateScript()), nil)
	if err != nil {
		return ChainHealth{}, err
	}
	return ParseChainHealth(out)
}

// Epoch is the chain's current epoch.
func (m *sshMachine) Epoch(ctx context.Context) (uint64, error) {
	out, err := m.capture(ctx, epochCommand(), nil)
	if err != nil {
		return 0, fmt.Errorf("read the chain's epoch on %s: %w", m.node.Host, err)
	}
	return ParseEpoch(strings.TrimSpace(out))
}
