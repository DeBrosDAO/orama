package nodeedit

import (
	"fmt"
	"io"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/install/installers"
)

const (
	// bytesPerGB is the decimal gigabyte Kubo's StorageMax counts in.
	bytesPerGB = 1_000_000_000
	// maxStorageGB bounds a declared capacity: a petabyte, far above any node and
	// far below where the byte count overflows. The node-side command holds the
	// same bound.
	maxStorageGB = 1_000_000
)

// Settings are the changes asked for. A nil field is left as it is.
type Settings struct {
	// StorageGB is the public storage capacity to declare, in GB.
	StorageGB *uint64
	// Exit makes the Tor relay an exit (true) or a plain relay (false).
	Exit *bool
	// Global asks for the global layer on (true) or off (false).
	Global *bool
}

// Empty reports whether nothing is asked for.
func (s Settings) Empty() bool { return s.StorageGB == nil && s.Exit == nil && s.Global == nil }

// Change is one thing the edit does to the node.
type Change struct {
	Description string
}

// Plan is what an edit will do to one node.
type Plan struct {
	Host string
	// Changes are the node's changes, in the order they are made.
	Changes []Change
	// Storage is the capacity to declare on the chain and on the node; nil when
	// storage is not changed. DeclareOnChain says the chain declares it too.
	Storage        *uint64
	DeclareOnChain bool
	// Exit is the relay role to set; nil when it is not changed.
	Exit *bool
}

// Nothing reports whether the node already is as asked.
func (p *Plan) Nothing() bool { return p.Storage == nil && p.Exit == nil }

// BuildPlan decides what to do to a node in state st for settings s, or why it
// cannot be done. chainNodeID names the node on the chain for the storage
// declaration; noChain resizes the node only.
func BuildPlan(host string, st NodeState, s Settings, chainNodeID string, noChain bool) (*Plan, error) {
	if s.Empty() {
		return nil, clierr.Usage("name what to change: --storage-gb, --exit or --global")
	}
	if noChain && chainNodeID != "" {
		return nil, clierr.Usage("--no-chain and --chain-node-id contradict each other: either the capacity is declared on the chain or it is not")
	}
	p := &Plan{Host: host}
	if err := p.planGlobal(st, s); err != nil {
		return nil, err
	}
	if err := p.planStorage(st, s, chainNodeID, noChain); err != nil {
		return nil, err
	}
	if err := p.planExit(st, s); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *Plan) planGlobal(st NodeState, s Settings) error {
	if s.Global == nil {
		return nil
	}
	switch {
	case *s.Global && st.Global:
		p.note("the global layer is already on")
	case !*s.Global && !st.Global:
		p.note("the global layer is already off")
	case *s.Global:
		return clierr.Conflict("%s has no global layer, and edit does not install it: it needs the network's genesis, the chain's state-sync and the operator's "+
			"registration on the chain, which the setup flow does when it is run again for this IP without --cluster-only. Nothing was changed", p.Host)
	default:
		return clierr.Conflict("edit does not turn the global layer off: the node holds the validator's consensus key and the node's bonds on the chain, "+
			"and removing the units would drop them without retiring either. To take the node out of the network, use `orama remove --node %s`. Nothing was changed", p.Host)
	}
	return nil
}

func (p *Plan) planStorage(st NodeState, s Settings, chainNodeID string, noChain bool) error {
	if s.StorageGB == nil {
		return nil
	}
	gb := *s.StorageGB
	switch {
	case !st.IPFS:
		return clierr.Conflict("%s has no public storage (its public Kubo is not installed), so there is no storage to resize. Nothing was changed", p.Host)
	case gb == 0 || gb > maxStorageGB:
		return clierr.Usage("--storage-gb must be between 1 and %d GB (use `orama remove` to stop providing storage)", maxStorageGB)
	case chainNodeID == "" && !noChain:
		return clierr.Usage("the capacity is also declared on the chain, for the node's id there: pass --chain-node-id <id>, or --no-chain to resize the node only")
	}
	if st.StorageMax == installers.PublicStorageMax(gb*bytesPerGB) {
		p.note(fmt.Sprintf("the public storage is already sized for %d GB", gb))
		return nil
	}
	p.Storage, p.DeclareOnChain = &gb, chainNodeID != ""
	desc := fmt.Sprintf("size the public Kubo for %d GB (StorageMax %s now) and restart it", gb, st.StorageMax)
	if p.DeclareOnChain {
		desc = fmt.Sprintf("declare %d GB of capacity for node %s on the chain (MsgDeclareCapacity, signed by your RootWallet), then ", gb, chainNodeID) + desc
	}
	p.Changes = append(p.Changes, Change{desc})
	return nil
}

func (p *Plan) planExit(st NodeState, s Settings) error {
	if s.Exit == nil {
		return nil
	}
	switch {
	case !st.Relay:
		return clierr.Conflict("%s runs no Tor relay, so it has no exit role to switch. Nothing was changed", p.Host)
	case st.Exit == *s.Exit:
		p.note(fmt.Sprintf("the relay already is %s", relayRole(*s.Exit)))
		return nil
	}
	p.Exit = s.Exit
	p.Changes = append(p.Changes, Change{fmt.Sprintf("switch the Tor relay to %s and restart it (its on-chain roles are not changed here: bond or unbond the exit role with `orama global bond`)", relayRole(*s.Exit))})
	return nil
}

// note records a request that needs no change.
func (p *Plan) note(msg string) { p.Changes = append(p.Changes, Change{"nothing to do: " + msg}) }

func relayRole(exit bool) string {
	if exit {
		return "an exit"
	}
	return "a plain relay"
}

// Render prints the plan.
func (p *Plan) Render(w io.Writer) {
	fmt.Fprintf(w, "Edit %s:\n", p.Host)
	for _, c := range p.Changes {
		fmt.Fprintf(w, "  - %s\n", c.Description)
	}
}
