package onchain

import (
	"context"
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/clusterreg"
)

// Defaults of a validator the operator creates without choosing. They are what
// the chain documents: the self bond is 1,000 ORAMA (x/power MinSelfBond), and a
// commission can start at 10% and be raised by at most a point a day to 20%.
const (
	// DefaultSelfBondNorama is 1,000 ORAMA; 1 ORAMA is 10^9 norama.
	DefaultSelfBondNorama = "1000000000000"
	// DefaultMinSelfDelegationNorama keeps x/staking's own minimum: the
	// operator may not unbond to nothing.
	DefaultMinSelfDelegationNorama = "1"
	DefaultCommissionRate          = "0.10"
	DefaultCommissionMaxRate       = "0.20"
	DefaultCommissionMaxStep       = "0.01"
)

// ValidatorSpec is what an operator says about the validator it creates. The
// consensus key is the node's. Every other field has a default.
type ValidatorSpec struct {
	Moniker         string
	Identity        string
	Website         string
	SecurityContact string
	Details         string
	// ConsensusPubKey is the 32-byte ed25519 key the node signs blocks with.
	ConsensusPubKey []byte
	// SelfBond is the norama the operator bonds. Empty means 1,000 ORAMA.
	SelfBond string
	// CommissionRate, CommissionMaxRate and CommissionMaxStep are decimals from
	// 0 to 1. Empty means the defaults.
	CommissionRate    string
	CommissionMaxRate string
	CommissionMaxStep string
	// MinSelfDelegation is in norama. Empty means 1.
	MinSelfDelegation string
}

// RegisterOperator registers the signing account as an operator
// (MsgRegisterOperator).
func (c *Client) RegisterOperator(ctx context.Context) (*Receipt, error) {
	operator, err := c.Operator(ctx)
	if err != nil {
		return nil, err
	}
	msg, err := clusterreg.EncodeRegisterOperator(operator)
	if err != nil {
		return nil, err
	}
	return c.sendMsg(ctx, "register the operator", clusterreg.RegisterOperatorTypeURL, msg)
}

// RegisterNode registers a node of the signing operator (MsgRegisterNode). n
// carries the node's facts and its signed bindings; its Operator is the signing
// account, and is filled in when empty.
func (c *Client) RegisterNode(ctx context.Context, n clusterreg.NodeRegistration) (*Receipt, error) {
	operator, err := c.operatorFor(ctx, n.Operator)
	if err != nil {
		return nil, err
	}
	n.Operator = operator
	if err := clusterreg.ValidateNode(n); err != nil {
		return nil, fmt.Errorf("register node %q: %w", n.NodeID, err)
	}
	return c.sendMsg(ctx, "register node "+n.NodeID, clusterreg.RegisterNodeTypeURL, clusterreg.EncodeRegisterNode(n))
}

// Bond bonds norama to one role of one of the signing operator's nodes
// (MsgBondNode).
func (c *Client) Bond(ctx context.Context, b clusterreg.Bond) (*Receipt, error) {
	operator, err := c.operatorFor(ctx, b.Operator)
	if err != nil {
		return nil, err
	}
	b.Operator = operator
	if err := clusterreg.ValidateBond(b); err != nil {
		return nil, fmt.Errorf("bond node %q: %w", b.NodeID, err)
	}
	return c.sendMsg(ctx, "bond node "+b.NodeID, clusterreg.BondNodeTypeURL, clusterreg.EncodeBond(b))
}

// DeclareCapacity declares the storage bytes of one of the signing operator's
// nodes (MsgDeclareCapacity).
func (c *Client) DeclareCapacity(ctx context.Context, capacity clusterreg.Capacity) (*Receipt, error) {
	operator, err := c.operatorFor(ctx, capacity.Operator)
	if err != nil {
		return nil, err
	}
	capacity.Operator = operator
	if err := clusterreg.ValidateCapacity(capacity); err != nil {
		return nil, fmt.Errorf("declare capacity of node %q: %w", capacity.NodeID, err)
	}
	return c.sendMsg(ctx, "declare capacity of node "+capacity.NodeID, clusterreg.DeclareCapacityTypeURL, clusterreg.EncodeCapacity(capacity))
}

// CreateValidator creates the signing operator's validator (MsgCreateValidator),
// bonding its own norama.
func (c *Client) CreateValidator(ctx context.Context, spec ValidatorSpec) (*Receipt, error) {
	operator, err := c.Operator(ctx)
	if err != nil {
		return nil, err
	}
	msg, err := clusterreg.EncodeCreateValidator(spec.create(operator))
	if err != nil {
		return nil, fmt.Errorf("create the validator: %w", err)
	}
	return c.sendMsg(ctx, "create the validator", clusterreg.CreateValidatorTypeURL, msg)
}

// create fills the defaults of a spec for operator.
func (s ValidatorSpec) create(operator string) clusterreg.ValidatorCreate {
	return clusterreg.ValidatorCreate{
		Operator: operator, Moniker: s.Moniker, Identity: s.Identity, Website: s.Website,
		SecurityContact: s.SecurityContact, Details: s.Details, ConsensusPubKey: s.ConsensusPubKey,
		SelfBond:          orDefault(s.SelfBond, DefaultSelfBondNorama),
		MinSelfDelegation: orDefault(s.MinSelfDelegation, DefaultMinSelfDelegationNorama),
		CommissionRate:    orDefault(s.CommissionRate, DefaultCommissionRate),
		CommissionMaxRate: orDefault(s.CommissionMaxRate, DefaultCommissionMaxRate),
		CommissionMaxStep: orDefault(s.CommissionMaxStep, DefaultCommissionMaxStep),
	}
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// operatorFor returns the signing account, and refuses a message that names
// another operator: only the operator's own key can sign it.
func (c *Client) operatorFor(ctx context.Context, named string) (string, error) {
	operator, err := c.Operator(ctx)
	if err != nil {
		return "", err
	}
	if named != "" && named != operator {
		return "", fmt.Errorf("the message is for operator %s but the RootWallet signs as %s", named, operator)
	}
	return operator, nil
}

func (c *Client) sendMsg(ctx context.Context, what, typeURL string, msg []byte) (*Receipt, error) {
	receipt, err := c.send(ctx, typeURL, msg)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	return receipt, nil
}
