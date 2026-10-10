// Package onchain sends the transactions an operator's setup needs: register
// the operator, register and bond its nodes, declare storage capacity and create
// its validator.
//
// A caller passes the operator's RootWallet agent and the facts of the node.
// Everything else a transaction needs is derived here, from the chain and the
// signer: the account number and sequence from the chain's account, the public
// key from the agent, the gas from a simulation of the transaction itself and
// the fee from x/fees' current base fee. Nothing is read from a flag.
package onchain

import (
	"context"

	"github.com/DeBrosOfficial/network/pkg/clusterreg"
	"github.com/DeBrosOfficial/network/pkg/rwagent"
)

// Chain is the part of a chain node's REST API a transaction needs.
type Chain interface {
	// Account reads an account's number and sequence.
	Account(ctx context.Context, address string) (clusterreg.Account, error)
	// BaseFee reads x/fees' base fee, norama per unit of gas.
	BaseFee(ctx context.Context) (string, error)
	// SimulateGas runs a transaction without including it and returns its gas.
	SimulateGas(ctx context.Context, tx []byte) (uint64, error)
	// Broadcast submits a signed transaction and returns its hash.
	Broadcast(ctx context.Context, tx []byte) (string, error)
	// WaitIncluded returns the height of the block holding the transaction, and
	// the chain's reason when that block refused it.
	WaitIncluded(ctx context.Context, hash string) (int64, error)
}

// Signer is the operator's RootWallet. *rwagent.Client is one.
type Signer interface {
	// OramaAccount returns the account the agent signs for.
	OramaAccount(ctx context.Context) (*rwagent.OramaAccount, error)
	// SignOramaTx signs a SIGN_MODE_DIRECT SignDoc; the agent shows the
	// transaction to its owner and may refuse it.
	SignOramaTx(ctx context.Context, signDoc []byte) (*rwagent.OramaTxSignature, error)
}

// REST is a Chain backed by a node's Cosmos REST API. Base is its root, for
// example http://127.0.0.1:31003. A context built with clusterreg.WithHTTPClient
// carries the requests through that client, which is how a submission goes over
// Tor.
type REST struct{ Base string }

func (r REST) Account(ctx context.Context, address string) (clusterreg.Account, error) {
	return clusterreg.FetchAccount(ctx, r.Base, address)
}

func (r REST) BaseFee(ctx context.Context) (string, error) {
	return clusterreg.FetchBaseFee(ctx, r.Base)
}

func (r REST) SimulateGas(ctx context.Context, tx []byte) (uint64, error) {
	return clusterreg.SimulateGas(ctx, r.Base, tx)
}

func (r REST) Broadcast(ctx context.Context, tx []byte) (string, error) {
	return clusterreg.Broadcast(ctx, r.Base, tx)
}

func (r REST) WaitIncluded(ctx context.Context, hash string) (int64, error) {
	return clusterreg.WaitIncluded(ctx, r.Base, hash, clusterreg.InclusionTimeout, clusterreg.InclusionPoll)
}
