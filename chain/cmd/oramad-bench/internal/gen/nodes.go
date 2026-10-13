package gen

import (
	"context"
	"errors"
	"fmt"

	"github.com/DeBrosOfficial/network/chain/client/node"
	"github.com/DeBrosOfficial/network/chain/client/tx"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/account"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/ops"
	nodestypes "github.com/DeBrosOfficial/network/chain/x/nodes/types"
)

// RegisterNode is the type name of the registration of a node.
const RegisterNode = "register-node"

const (
	// nodeEndpointFormat is the public endpoint a node registers; it must be a public name.
	nodeEndpointFormat = "https://%s.bench.example.com"
	// nodeRegion is the region hint every node registers with.
	nodeRegion = "bench"
	// hotKeyDomain separates the hot keys of a run's nodes from its bench accounts.
	hotKeyDomain = "oramad-bench-hot"
)

func init() { register(RegisterNode, func() Generator { return &registerNode{} }) }

// registerNode registers one node per transaction, each with its own hot key and a binding the hot
// key signs, under an operator that is a bench account.
type registerNode struct {
	chainID  string
	runID    string
	accounts []string
}

func (r *registerNode) Name() string { return RegisterNode }

func (r *registerNode) Describe() string {
	return "x/nodes MsgRegisterNode: a STORAGE node with its own hot key and a signed hot-key binding, under a bench account as operator"
}

func (r *registerNode) Prepare(ctx context.Context, env *Env, _ int) error {
	r.chainID, r.runID = env.Chain.ChainID, env.RunID
	r.accounts = make([]string, len(env.Accounts))
	for i, a := range env.Accounts {
		r.accounts[i] = a.Address
	}
	return ops.Each(ctx, len(env.Accounts), env.Parallel, func(ctx context.Context, i int) error {
		return registerOperator(ctx, env, env.Accounts[i])
	})
}

// registerOperator registers acct as an operator unless it already is one.
func registerOperator(ctx context.Context, env *Env, acct tx.Account) error {
	var resp nodestypes.QueryOperatorResponse
	err := env.Chain.Client.Query(ctx, "/orama.nodes.v1.Query/Operator", &nodestypes.QueryOperatorRequest{Address: acct.Address}, &resp)
	if err == nil {
		return nil
	}
	var refusal *node.QueryError
	if !errors.As(err, &refusal) || !refusal.NotFound() {
		return fmt.Errorf("look up operator %s: %w", acct.Address, err)
	}
	if _, err := env.Chain.Client.Submit(ctx, acct, &nodestypes.MsgRegisterOperator{Operator: acct.Address}); err != nil {
		return fmt.Errorf("register operator %s: %w", acct.Address, err)
	}
	return nil
}

func (r *registerNode) Available() int { return Unlimited }

func (r *registerNode) Plan(k int) (Planned, error) {
	acct := roundRobin(k, len(r.accounts))
	operator := r.accounts[acct]
	hot, err := account.Derive(fmt.Sprintf("%s|%s", hotKeyDomain, r.runID), k)
	if err != nil {
		return Planned{}, err
	}
	pub := hot.PublicKey().Bytes()
	sig, err := hot.Sign(nodestypes.BindingSignBytes(r.chainID, operator, nodestypes.HotKeyService, pub))
	if err != nil {
		return Planned{}, fmt.Errorf("sign the hot-key binding: %w", err)
	}
	id := fmt.Sprintf("bench-%s-%d", r.runID, k)
	msg := &nodestypes.MsgRegisterNode{
		Operator: operator, NodeId: id, Roles: []nodestypes.Role{nodestypes.RoleStorage}, HotKey: hot.Address,
		Bindings:   []nodestypes.Binding{{Service: nodestypes.HotKeyService, KeyType: nodestypes.KeyTypeSecp256k1, Pubkey: pub, Signature: sig}},
		Endpoints:  []string{fmt.Sprintf(nodeEndpointFormat, id)},
		RegionHint: nodeRegion,
	}
	return Planned{Account: acct, Msg: msg}, nil
}
