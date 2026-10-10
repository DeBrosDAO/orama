package setup

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strconv"

	"github.com/DeBrosOfficial/network/pkg/clusterreg"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/onchain"
)

// onchainPhase registers the operator and every full node on the chain, in order
// and one transaction at a time: they are all signed by one account, whose
// sequence number each transaction takes. A step the chain already shows is not
// sent again, so a run that stopped half way resumes where it did.
func (r *runner) onchainPhase(ctx context.Context) error {
	full := r.fullRuns()
	// The transactions go through the same client as the reads: no redirects.
	ctx = clusterreg.WithHTTPClient(ctx, chainHTTPClient())
	sess, err := r.d.Chain.Open(ctx, full[0].m, r.net.Manifest.ChainID)
	if err != nil {
		return fmt.Errorf("reach the chain through %s: %w", full[0].plan.IP, err)
	}
	defer sess.Close()
	if err := r.checkNamesFree(ctx, sess, full); err != nil {
		return err
	}
	budget, err := r.fund(ctx, sess)
	if err != nil {
		return err
	}
	if err := r.registerOperator(ctx, sess); err != nil {
		return err
	}
	for _, n := range full {
		if err := r.registerNode(ctx, sess, n, budget.Bonds[n.plan.Name]); err != nil {
			r.emit(n.plan.IP, StepOnchain, StateFailed, err.Error())
			return fmt.Errorf("machine %s: %w", n.plan.IP, err)
		}
		if err := r.claimName(ctx, sess, n); err != nil {
			return err
		}
	}
	return nil
}

// checkNamesFree asks the chain, before the run signs or spends anything, who holds each
// node's name. A name another node or operator holds would be refused at the end of the run,
// after the registrations and the bonds; a name this node of this operator holds is done.
func (r *runner) checkNamesFree(ctx context.Context, sess ChainSession, full []*nodeRun) error {
	for _, n := range full {
		holder, err := sess.NameHolder(ctx, n.plan.Name)
		if err != nil {
			return fmt.Errorf("check who holds the name %q: %w", n.plan.Name, err)
		}
		if holder != nil && (holder.NodeID != n.plan.Name || holder.Operator != r.oper) {
			return fmt.Errorf("machine %s: the name %q is held by node %q of operator %s, not by this node of %s: choose another --name (nothing was sent)",
				n.plan.IP, n.plan.Name, holder.NodeID, holder.Operator, r.oper)
		}
	}
	return nil
}

// fund makes sure the operator account holds what the run bonds and spends. On a
// network with a faucet the shortfall is requested; elsewhere the run stops and
// says exactly what to send.
func (r *runner) fund(ctx context.Context, sess ChainSession) (*Budget, error) {
	params, err := sess.Params(ctx)
	if err != nil {
		return nil, fmt.Errorf("read the chain's parameters: %w", err)
	}
	budget, err := ComputeBudget(r.plan, params)
	if err != nil {
		return nil, err
	}
	r.d.Report.Linef("this setup bonds %s ORAMA, self-bonds %s ORAMA for the validator, sets aside %s ORAMA for fees and deposits and moves %s ORAMA to the fee balances of the storage providers' hot keys",
		Orama(bondTotal(budget)), Orama(budget.SelfBond), Orama(budget.Reserve), Orama(budget.HotKeys))
	have, err := sess.Balance(ctx, r.oper)
	if err != nil {
		return nil, fmt.Errorf("read the balance of %s: %w", r.oper, err)
	}
	need, err := r.remainingNeed(ctx, sess, budget)
	if err != nil {
		return nil, err
	}
	if have.Cmp(need) >= 0 {
		return budget, nil
	}
	notFunded := &NotFundedError{Address: r.oper, Have: have, Need: need, Budget: budget}
	funder := r.d.Funder
	if r.create != nil && r.d.CreateFunder != nil {
		funder = r.d.CreateFunder
	}
	if !r.net.Manifest.Faucet || funder == nil {
		return nil, notFunded
	}
	shortfall := new(big.Int).Sub(need, have)
	r.d.Report.Linef("asking the %s faucet for %s ORAMA for %s", r.net.Manifest.Name, Orama(shortfall), r.oper)
	if err := funder.Fund(ctx, r.net.Manifest, r.oper, shortfall); err != nil {
		notFunded.FaucetTried = err.Error()
		return nil, notFunded
	}
	if have, err = r.awaitFunds(ctx, sess, need); err != nil {
		return nil, err
	}
	if have.Cmp(need) < 0 {
		notFunded.Have = have
		notFunded.FaucetTried = "it paid less than the setup needs, or the node setup reads the chain through has not seen the drip"
		return nil, notFunded
	}
	return budget, nil
}

// awaitFunds reads the operator's balance through the session until it holds need, and returns what it
// last read. The seed that paid answered when the drip was in a block it had seen, and this node can
// be a block behind it, so a balance that is short at the first read is not a drip that was too small.
// A balance still short at the deadline is returned for the caller to report; only a read that fails
// or a cancelled run is an error.
func (r *runner) awaitFunds(ctx context.Context, sess ChainSession, need *big.Int) (*big.Int, error) {
	var have *big.Int
	var readErr error
	_ = pollUntil(ctx, r.d.Timing.BalancePoll, r.d.Timing.BalanceDeadline, "the faucet's drip to reach "+r.oper, func(ctx context.Context) (bool, error) {
		balance, err := sess.Balance(ctx, r.oper)
		if readErr = err; err != nil {
			return false, err
		}
		have = balance
		return have.Cmp(need) >= 0, nil
	})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// A read that was still in flight when this wait's own deadline passed is the deadline, not a
	// failed read: the balance last seen is reported.
	if readErr != nil && !(have != nil && errors.Is(readErr, context.DeadlineExceeded)) {
		return nil, fmt.Errorf("read the balance of %s after the faucet: %w", r.oper, readErr)
	}
	return have, nil
}

func (r *runner) registerOperator(ctx context.Context, sess ChainSession) error {
	registered, err := sess.OperatorRegistered(ctx, r.oper)
	if err != nil {
		return fmt.Errorf("check whether %s is a registered operator: %w", r.oper, err)
	}
	if registered {
		r.emit("", StepOnchain, StateSkipped, "operator "+r.oper+" is registered")
		return nil
	}
	if _, err := sess.RegisterOperator(ctx); err != nil {
		return fmt.Errorf("register the operator %s: %w", r.oper, err)
	}
	r.emit("", StepOnchain, StateDone, "registered operator "+r.oper)
	return nil
}

// registerNode registers one node, bonds its roles, declares its capacity, starts
// the services that need the registration, funds its hot key and creates the
// validator when the node carries it. Each step first asks the chain whether it is already done.
func (r *runner) registerNode(ctx context.Context, sess ChainSession, n *nodeRun, bonds map[int]*big.Int) error {
	ip, id := n.plan.IP, n.plan.Name
	r.emit(ip, StepOnchain, StateRunning, "")
	ident, err := n.m.Identity(ctx, IdentityRequest{ChainID: r.net.Manifest.ChainID, Operator: r.oper, BindConsensus: n.plan.BindConsensus})
	if err != nil {
		return fmt.Errorf("read the node's keys: %w", err)
	}
	consensus, err := r.consensusBindingOf(n, ident)
	if err != nil {
		return err
	}
	node, err := sess.Node(ctx, id)
	if err != nil {
		return fmt.Errorf("check whether node %q is registered: %w", id, err)
	}
	if node == nil {
		if err := r.sendRegistration(ctx, sess, n, ident, consensus); err != nil {
			return err
		}
		node = &RegisteredNode{Bonds: map[int]*big.Int{}}
	} else if err := r.bindConsensus(ctx, sess, n, node, consensus); err != nil {
		return err
	}
	if err := r.bondRoles(ctx, sess, n, node, bonds); err != nil {
		return err
	}
	if want := StorageCapacityBytes(n.plan.StorageGB); node.CapacityBytes != want {
		if _, err := sess.DeclareCapacity(ctx, clusterreg.Capacity{NodeID: id, Bytes: want}); err != nil {
			return fmt.Errorf("declare %d GB of capacity for node %q: %w", n.plan.StorageGB, id, err)
		}
	}
	if err := n.m.StartServices(ctx, id); err != nil {
		return fmt.Errorf("start the node's services: %w", err)
	}
	if err := r.fundHotKey(ctx, sess, n, ident.HotKey); err != nil {
		return err
	}
	if n.plan.Validator {
		if err := r.createValidator(ctx, sess, n, ident); err != nil {
			return err
		}
	}
	r.emit(ip, StepOnchain, StateDone, "node "+id)
	return nil
}

// fundHotKey gives the node's hot key the fee-only balance its storage provider pays proof fees
// from, out of the operator's bank balance. An operator that runs no validator of its own earns
// nothing until its providers have proven, so earnings cannot start the first proof. A hot key whose
// balance already covers hotKeyFundingPerNode is left alone; one that holds part of it is given the
// difference, so a run that stopped half way resumes and a run repeated does not pay twice.
func (r *runner) fundHotKey(ctx context.Context, sess ChainSession, n *nodeRun, hotKey string) error {
	id := n.plan.Name
	have, err := sess.FeeBalance(ctx, hotKey)
	if err != nil {
		return fmt.Errorf("read the fee balance of the hot key %s of node %q: %w", hotKey, id, err)
	}
	want := big.NewInt(hotKeyFundingPerNode)
	if have.Cmp(want) >= 0 {
		r.emit(n.plan.IP, StepOnchain, StateSkipped, "the hot key of node "+id+" holds "+Orama(have)+" ORAMA of fee balance")
		return nil
	}
	delta := new(big.Int).Sub(want, have)
	if _, err := sess.FundHotKey(ctx, clusterreg.HotKeyFunding{NodeID: id, Amount: delta.String(), FromBank: true}); err != nil {
		return fmt.Errorf("fund the hot key %s of node %q with %s ORAMA from the operator's bank balance: %w", hotKey, id, Orama(delta), err)
	}
	return nil
}

func (r *runner) sendRegistration(ctx context.Context, sess ChainSession, n *nodeRun, ident NodeIdentity, consensus *clusterreg.NodeBinding) error {
	asn, err := r.asnOf(ctx, n.plan.IP)
	if err != nil {
		return err
	}
	bindings := []clusterreg.NodeBinding{ident.HotBinding}
	if consensus != nil {
		bindings = append(bindings, *consensus)
	}
	reg := clusterreg.NodeRegistration{
		Operator: r.oper, NodeID: n.plan.Name, Roles: n.plan.Roles, HotKey: ident.HotKey,
		Bindings:  bindings,
		Endpoints: []string{"http://" + n.plan.IP + ":" + strconv.Itoa(constants.GlobalProviderPort)},
		ASN:       asn,
	}
	if _, err := sess.RegisterNode(ctx, reg); err != nil {
		return fmt.Errorf("register node %q: %w", n.plan.Name, err)
	}
	return nil
}

// asnOf is the autonomous system number declared for ip: --asn, else looked up.
// A lookup that fails is an error naming the flag, not an undeclared node: a node
// with no ASN gets no deal slot, and the operator should choose that.
func (r *runner) asnOf(ctx context.Context, ip string) (uint32, error) {
	if r.opts.ASNSet {
		return r.opts.ASN, nil
	}
	if r.d.ASN == nil {
		return 0, errors.New("no way to look up the autonomous system number: pass --asn <number> (or --asn 0 to leave it undeclared)")
	}
	asn, err := r.d.ASN(ctx, ip)
	if err != nil {
		return 0, fmt.Errorf("look up the autonomous system number of %s: %w (pass --asn <number>, or --asn 0 to leave it undeclared)", ip, err)
	}
	return asn, nil
}

// bondRoles bonds each role up to its target, sending only the difference.
func (r *runner) bondRoles(ctx context.Context, sess ChainSession, n *nodeRun, node *RegisteredNode, targets map[int]*big.Int) error {
	for _, role := range n.plan.Roles {
		target := targets[role]
		have := node.Bonds[role]
		if have == nil {
			have = new(big.Int)
		}
		if have.Cmp(target) >= 0 {
			continue
		}
		delta := new(big.Int).Sub(target, have)
		if _, err := sess.Bond(ctx, clusterreg.Bond{NodeID: n.plan.Name, Role: role, Amount: delta.String()}); err != nil {
			return fmt.Errorf("bond %s ORAMA to role %d of node %q: %w", Orama(delta), role, n.plan.Name, err)
		}
	}
	return nil
}

func (r *runner) createValidator(ctx context.Context, sess ChainSession, n *nodeRun, ident NodeIdentity) error {
	exists, err := sess.ValidatorExists(ctx, r.oper)
	if err != nil {
		return fmt.Errorf("check whether %s has a validator: %w", r.oper, err)
	}
	if exists {
		r.emit(n.plan.IP, StepOnchain, StateSkipped, "the operator's validator exists")
		return nil
	}
	if _, err := sess.CreateValidator(ctx, onchain.ValidatorSpec{Moniker: n.plan.Name, ConsensusPubKey: ident.ConsensusPubKey}); err != nil {
		return fmt.Errorf("create the validator %q: %w", n.plan.Name, err)
	}
	return nil
}

// claimName claims the node's identification name, which the network's own cluster
// serves as <name>.nodes.<domain>: the name is the node's name in the plan, which is
// also its id on the chain.
func (r *runner) claimName(ctx context.Context, sess ChainSession, n *nodeRun) error {
	if err := r.d.Names.Claim(ctx, sess, n.plan.Name, n.plan.Name); err != nil {
		r.emit(n.plan.IP, StepName, StateFailed, err.Error())
		return fmt.Errorf("machine %s: claim the name %q: %w", n.plan.IP, n.plan.Name, err)
	}
	r.emit(n.plan.IP, StepName, StateDone, n.plan.Name)
	return nil
}

// remainingNeed is what the account still has to hold: the budget less the bonds
// the chain already shows, the validator if it exists, the reserve of every
// node that is already registered and the deposit of every name already held. A run that stopped after bonding is not asked
// for the bonds again.
func (r *runner) remainingNeed(ctx context.Context, sess ChainSession, b *Budget) (*big.Int, error) {
	need := new(big.Int).Set(b.Total)
	for _, n := range r.fullRuns() {
		node, err := sess.Node(ctx, n.plan.Name)
		if err != nil {
			return nil, fmt.Errorf("check whether node %q is registered: %w", n.plan.Name, err)
		}
		if node == nil {
			continue
		}
		need.Sub(need, big.NewInt(feeReservePerNode))
		funded, err := hotKeyFunded(ctx, sess, node)
		if err != nil {
			return nil, err
		}
		need.Sub(need, funded)
		for role, target := range b.Bonds[n.plan.Name] {
			if have := node.Bonds[role]; have != nil {
				need.Sub(need, minInt(have, target))
			}
		}
		held, err := sess.NodeName(ctx, n.plan.Name)
		if err != nil {
			return nil, fmt.Errorf("check whether node %q holds a name: %w", n.plan.Name, err)
		}
		if held != "" {
			need.Sub(need, b.NameDeposit)
		}
	}
	if exists, err := sess.ValidatorExists(ctx, r.oper); err != nil {
		return nil, fmt.Errorf("check whether %s has a validator: %w", r.oper, err)
	} else if exists {
		need.Sub(need, b.SelfBond)
	}
	if need.Sign() < 0 {
		need.SetInt64(0)
	}
	return need, nil
}

// hotKeyFunded is how much of hotKeyFundingPerNode the node's hot key already holds as fee balance.
func hotKeyFunded(ctx context.Context, sess ChainSession, node *RegisteredNode) (*big.Int, error) {
	if node.HotKey == "" {
		return new(big.Int), nil
	}
	have, err := sess.FeeBalance(ctx, node.HotKey)
	if err != nil {
		return nil, fmt.Errorf("read the fee balance of the hot key %s: %w", node.HotKey, err)
	}
	return minInt(have, big.NewInt(hotKeyFundingPerNode)), nil
}

func minInt(a, b *big.Int) *big.Int {
	if a.Cmp(b) < 0 {
		return a
	}
	return b
}
