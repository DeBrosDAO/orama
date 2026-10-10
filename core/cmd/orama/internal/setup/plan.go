package setup

import (
	"fmt"
	"slices"
	"strings"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/clusterreg"
	"github.com/DeBrosOfficial/network/pkg/install"
	"github.com/DeBrosOfficial/network/pkg/netregistry"
)

// ClusterRole is what a machine does in the cluster.
type ClusterRole string

const (
	// ClusterCreate starts the cluster: the machine is its genesis node.
	ClusterCreate ClusterRole = "create"
	// ClusterJoin joins a cluster that exists, through an invite.
	ClusterJoin ClusterRole = "join"
)

// NodePlan is what one IP gets.
type NodePlan struct {
	IP      string
	Name    string
	Profile install.Profile
	Cluster ClusterRole
	// Services are the global services installed beside the cluster node; empty
	// for cluster-only.
	Services []install.GlobalService
	Exit     bool
	// Roles are the on-chain roles the node registers (clusterreg.Role*).
	Roles []int
	// Validator says this node's consensus key becomes the operator's validator.
	// An operator has one validator, the first full node's.
	Validator bool
	StorageGB uint64
}

// Full reports whether the node gets the global layer.
func (n NodePlan) Full() bool { return n.Profile == install.ProfileFull }

// HasService reports whether the node installs s.
func (n NodePlan) HasService(s install.GlobalService) bool { return slices.Contains(n.Services, s) }

// Plan is everything a run does, decided before any machine is touched.
type Plan struct {
	Network string
	ChainID string
	Channel string
	// Env is the CLI environment the cluster is recorded under.
	Env string
	// Domain is the private cluster's base domain, if it has one.
	Domain string
	// JoinsExisting is true when the environment already has cluster nodes, so
	// every IP joins.
	JoinsExisting bool
	Nodes         []NodePlan
	// Notes are things the operator is told before confirming: a role that was
	// left out and why.
	Notes []string
}

// PlanInput is what the plan is a function of.
type PlanInput struct {
	Options Options
	Network *netregistry.Manifest
	// Env is the environment the cluster is recorded under, decided by the
	// caller from Options.Env and the configured environments.
	Env string
	// ExistingHosts are the cluster nodes the environment already records.
	ExistingHosts []string
}

// BuildPlan decides, for each IP, whether it creates or joins the cluster,
// whether it gets the global layer, which services and on-chain roles that
// means, and which node is the validator.
func BuildPlan(in PlanInput) (*Plan, error) {
	o := in.Options
	if in.Network == nil {
		return nil, fmt.Errorf("no network to plan for")
	}
	if len(o.IPs) == 0 {
		return nil, clierr.Usage("no machines to plan for")
	}
	names, err := planNames(o)
	if err != nil {
		return nil, err
	}
	p := &Plan{
		Network: in.Network.Name, ChainID: in.Network.ChainID, Channel: in.Network.Channel,
		Env: in.Env, Domain: o.Domain, JoinsExisting: len(in.ExistingHosts) > 0,
	}
	validatorTaken := false
	for i, ip := range o.IPs {
		n := NodePlan{IP: ip, Name: names[i], Profile: install.ProfileClusterOnly, Cluster: ClusterJoin}
		if i == 0 && !p.JoinsExisting {
			n.Cluster = ClusterCreate
		}
		if !o.ClusterOnly {
			n.Profile, n.StorageGB, n.Exit = install.ProfileFull, o.StorageGB, o.Exit
			n.Services, n.Roles = globalServices(o)
			if !validatorTaken && !o.NoValidator {
				n.Validator, validatorTaken = true, true
			}
		}
		p.Nodes = append(p.Nodes, n)
	}
	if !o.ClusterOnly && o.TorNetwork == "" {
		p.Notes = append(p.Notes, "no relay: the Tor network file (--tor-network) is not given, so the nodes run the chain and the public storage only")
	}
	return p, nil
}

// planNames names every IP: the one name for a single full node, name-N for
// several. A cluster-only run needs no name, and without one the nodes are named
// after their address in the summary only.
func planNames(o Options) ([]string, error) {
	if o.Name == "" {
		return make([]string, len(o.IPs)), nil
	}
	names := NodeNames(o.Name, len(o.IPs))
	for _, n := range names {
		if err := ValidateNodeName(n); err != nil {
			return nil, clierr.Usage("--name: %v (the nodes are named %s)", err, strings.Join(names, ", "))
		}
	}
	return names, nil
}

// globalServices are the services of the full profile and the on-chain roles
// that go with them. The chain is on every node; the public IPFS and its
// provider are the storage role; the relay is installed when the Tor network
// file is at hand, and is an exit only on request.
func globalServices(o Options) ([]install.GlobalService, []int) {
	services := []install.GlobalService{install.GlobalServiceChain, install.GlobalServiceIPFS, install.GlobalServiceProvider}
	roles := []int{clusterreg.RoleStorage}
	if o.TorNetwork != "" {
		services = append(services, install.GlobalServiceRelay)
		roles = append(roles, clusterreg.RoleRelay)
		if o.Exit {
			roles = append(roles, clusterreg.RoleExit)
		}
	}
	return services, roles
}

// ServiceNames are the node's --services values for `orama global install`:
// the services, and exit beside relay.
func (n NodePlan) ServiceNames() []string {
	names := make([]string, 0, len(n.Services)+1)
	for _, s := range n.Services {
		names = append(names, string(s))
	}
	if n.Exit {
		names = append(names, "exit")
	}
	return names
}

// Summary is the plan as the lines the operator confirms.
func (p *Plan) Summary() []string {
	lines := []string{fmt.Sprintf("network %s (chain %s, channel %s)", p.Network, p.ChainID, p.Channel)}
	if p.JoinsExisting {
		lines = append(lines, fmt.Sprintf("adds to the cluster already recorded as %q; use --env for a cluster of its own", p.Env))
	}
	if p.Domain != "" {
		lines = append(lines, "cluster domain "+p.Domain)
	}
	for _, n := range p.Nodes {
		lines = append(lines, nodeSummary(n))
	}
	return append(lines, p.Notes...)
}

func nodeSummary(n NodePlan) string {
	what := "cluster node joins the cluster"
	if n.Cluster == ClusterCreate {
		what = "cluster node creates the cluster"
	}
	label := n.IP
	if n.Name != "" {
		label = n.Name + " (" + n.IP + ")"
	}
	if !n.Full() {
		return fmt.Sprintf("%s: %s only", label, what)
	}
	extras := []string{"chain", fmt.Sprintf("%d GB public storage", n.StorageGB)}
	if n.HasService(install.GlobalServiceRelay) {
		kind := "relay"
		if n.Exit {
			kind = "EXIT relay"
		}
		extras = append(extras, kind)
	}
	if n.Validator {
		extras = append(extras, "validator")
	}
	return fmt.Sprintf("%s: %s, global layer: %s", label, what, strings.Join(extras, ", "))
}
