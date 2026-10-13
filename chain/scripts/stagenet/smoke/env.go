package main

import (
	"context"
	"fmt"
	"sync"

	wasmtypes "github.com/CosmWasm/wasmd/x/wasm/types"
	rpchttp "github.com/cometbft/cometbft/rpc/client/http"

	"github.com/DeBrosOfficial/network/chain/client/node"
	nodestypes "github.com/DeBrosOfficial/network/chain/x/nodes/types"
	shieldedtypes "github.com/DeBrosOfficial/network/chain/x/shielded/types"
)

const rawRPCTimeoutSeconds = 15

// nodeRef is one stagenet node: its label, its ssh alias and its public address.
type nodeRef struct {
	Name  string
	Alias string
	IP    string
}

// env is what every check runs against: the nodes, a way to reach each one's namespace-local RPC,
// and (once started) the signing agent of the first node's operator.
type env struct {
	chainID  string
	nodes    []nodeRef
	ssh      *sshRunner
	orama    string
	caFile   string
	gateway  string
	workDir  string
	scenario string
	// voteExtHeight is the genesis vote_extensions_enable_height.
	voteExtHeight int64

	mu      sync.Mutex
	rpcAddr map[string]string
	restAdr map[string]string
	clients map[string]*node.Client
	raw     map[string]*rpchttp.HTTP

	signer *remoteSigner
	// agentSocket is the local end of the forwarded agent socket, for `orama` commands.
	agentSocket string
}

// rpcOf is the local address of a tunnel to the node's RPC, opened on first use.
func (e *env) rpcOf(ctx context.Context, n nodeRef) (string, error) {
	return e.tunnelOf(ctx, n, e.rpcAddr, remoteRPCPort)
}

// restOf is the local address of a tunnel to the node's REST API.
func (e *env) restOf(ctx context.Context, n nodeRef) (string, error) {
	return e.tunnelOf(ctx, n, e.restAdr, remoteRESTPort)
}

func (e *env) tunnelOf(ctx context.Context, n nodeRef, cache map[string]string, port int) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if addr, ok := cache[n.Name]; ok {
		return addr, nil
	}
	addr, err := e.ssh.tunnel(ctx, n.Alias, port)
	if err != nil {
		return "", err
	}
	cache[n.Name] = addr
	return addr, nil
}

// client is a chain client for the node that can also submit x/nodes, x/wasm and x/shielded
// messages.
func (e *env) client(ctx context.Context, n nodeRef) (*node.Client, error) {
	addr, err := e.rpcOf(ctx, n)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if c, ok := e.clients[n.Name]; ok {
		return c, nil
	}
	c, err := node.DialWith("tcp://"+addr, nodestypes.RegisterInterfaces, wasmtypes.RegisterInterfaces, shieldedtypes.RegisterInterfaces)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", n.Name, err)
	}
	e.clients[n.Name] = c
	return c, nil
}

// rawClient is a CometBFT RPC client for queries whose response type is only known by name.
func (e *env) rawClient(ctx context.Context, n nodeRef) (*rpchttp.HTTP, error) {
	addr, err := e.rpcOf(ctx, n)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if c, ok := e.raw[n.Name]; ok {
		return c, nil
	}
	c, err := rpchttp.NewWithTimeout("tcp://"+addr, "/websocket", rawRPCTimeoutSeconds)
	if err != nil {
		return nil, fmt.Errorf("open the RPC of %s: %w", n.Name, err)
	}
	e.raw[n.Name] = c
	return c, nil
}

func newEnv() *env {
	return &env{
		rpcAddr: map[string]string{}, restAdr: map[string]string{},
		clients: map[string]*node.Client{}, raw: map[string]*rpchttp.HTTP{},
	}
}
