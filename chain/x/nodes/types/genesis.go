package types

import (
	"fmt"

	"cosmossdk.io/math"
)

// DefaultGenesisState returns an empty x/nodes genesis with DefaultParams.
func DefaultGenesisState() *GenesisState {
	return &GenesisState{
		Params:         DefaultParams(),
		Operators:      []Operator{},
		Nodes:          []Node{},
		Clusters:       []Cluster{},
		Unbondings:     []UnbondingEntry{},
		RevokedPubkeys: []RevokedPubkey{},
		ServiceDays:    []ServiceDay{},
	}
}

// Validate checks genesis state that does not depend on the bank balance or
// the chain id. InitGenesis checks those.
func (gs GenesisState) Validate() error {
	if err := gs.Params.Validate(); err != nil {
		return err
	}
	operators := make(map[string]struct{}, len(gs.Operators))
	for _, op := range gs.Operators {
		addr, err := CanonicalAddress(op.Address)
		if err != nil {
			return fmt.Errorf("operator: %w", err)
		}
		if addr != op.Address {
			return fmt.Errorf("operator %q is not canonical (want %s)", op.Address, addr)
		}
		if _, ok := operators[addr]; ok {
			return fmt.Errorf("duplicate operator %s", addr)
		}
		operators[addr] = struct{}{}
	}

	revoked := make(map[string]struct{}, len(gs.RevokedPubkeys))
	for _, rev := range gs.RevokedPubkeys {
		if len(rev.Pubkey) == 0 {
			return fmt.Errorf("revoked pubkey is empty")
		}
		if rev.Reason != RevocationRetired && rev.Reason != RevocationTombstoned {
			return fmt.Errorf("revoked pubkey has reason %s", rev.Reason)
		}
		key := fmt.Sprintf("%x", rev.Pubkey)
		if _, ok := revoked[key]; ok {
			return fmt.Errorf("duplicate revoked pubkey %s", key)
		}
		revoked[key] = struct{}{}
	}

	nodes := make(map[string]Node, len(gs.Nodes))
	live := make(map[string]string, len(gs.Nodes))
	for _, node := range gs.Nodes {
		if err := validateGenesisNode(node, gs.Params, operators, revoked, live); err != nil {
			return err
		}
		if _, ok := nodes[node.NodeId]; ok {
			return fmt.Errorf("duplicate node %s", node.NodeId)
		}
		nodes[node.NodeId] = node
		for _, binding := range node.Bindings {
			live[fmt.Sprintf("%x", binding.Pubkey)] = node.NodeId
		}
	}

	clusters := make(map[string]struct{}, len(gs.Clusters))
	for _, cluster := range gs.Clusters {
		if err := validateGenesisCluster(cluster, gs.Params, operators); err != nil {
			return err
		}
		if _, ok := clusters[cluster.ClusterId]; ok {
			return fmt.Errorf("duplicate cluster %s", cluster.ClusterId)
		}
		clusters[cluster.ClusterId] = struct{}{}
	}

	seenUnbond := make(map[uint64]struct{}, len(gs.Unbondings))
	var maxID uint64
	for _, entry := range gs.Unbondings {
		node, ok := nodes[entry.NodeId]
		if !ok {
			return fmt.Errorf("unbonding %d references unknown node %s", entry.Id, entry.NodeId)
		}
		if entry.Operator != node.Operator {
			return fmt.Errorf("unbonding %d operator %s does not own node %s", entry.Id, entry.Operator, entry.NodeId)
		}
		if !HasRole(node.Roles, entry.Role) {
			return fmt.Errorf("unbonding %d role %s is not on node %s", entry.Id, entry.Role, entry.NodeId)
		}
		if err := PositiveAmount(entry.Amount); err != nil {
			return fmt.Errorf("unbonding %d: %w", entry.Id, err)
		}
		if entry.CompletionUnix <= 0 {
			return fmt.Errorf("unbonding %d completion must be positive", entry.Id)
		}
		if _, ok := seenUnbond[entry.Id]; ok {
			return fmt.Errorf("duplicate unbonding id %d", entry.Id)
		}
		seenUnbond[entry.Id] = struct{}{}
		if entry.Id >= maxID {
			maxID = entry.Id + 1
			if maxID == 0 {
				return fmt.Errorf("unbonding id space is exhausted")
			}
		}
	}
	if len(gs.Unbondings) > 0 && gs.NextUnbondingId < maxID {
		return fmt.Errorf("next_unbonding_id %d is not above existing ids (need >= %d)", gs.NextUnbondingId, maxID)
	}

	days := make(map[string]struct{}, len(gs.ServiceDays))
	for _, day := range gs.ServiceDays {
		addr, err := CanonicalAddress(day.Operator)
		if err != nil {
			return fmt.Errorf("service day: %w", err)
		}
		if addr != day.Operator {
			return fmt.Errorf("service day operator %q is not canonical", day.Operator)
		}
		if _, ok := operators[addr]; !ok {
			return fmt.Errorf("service day for unknown operator %s", addr)
		}
		if day.VolumeBytes < gs.Params.MinServiceVolumeBytes && !day.Relay {
			return fmt.Errorf("service day %s/%d does not meet the volume floor and is not relay service", addr, day.DayIndex)
		}
		key := fmt.Sprintf("%s/%d", addr, day.DayIndex)
		if _, ok := days[key]; ok {
			return fmt.Errorf("duplicate service day %s", key)
		}
		days[key] = struct{}{}
	}
	return nil
}

func validateGenesisNode(node Node, p Params, operators, revoked map[string]struct{}, live map[string]string) error {
	if err := ValidateID(node.NodeId); err != nil {
		return fmt.Errorf("node: %w", err)
	}
	if _, ok := operators[node.Operator]; !ok {
		return fmt.Errorf("node %s operator %s is not registered", node.NodeId, node.Operator)
	}
	if _, err := CanonicalAddress(node.Operator); err != nil {
		return fmt.Errorf("node %s: %w", node.NodeId, err)
	}
	hot, err := CanonicalAddress(node.HotKey)
	if err != nil {
		return fmt.Errorf("node %s hot key: %w", node.NodeId, err)
	}
	if hot != node.HotKey {
		return fmt.Errorf("node %s hot key is not canonical", node.NodeId)
	}
	if hot == node.Operator {
		return fmt.Errorf("node %s: %w", node.NodeId, ErrHotKey)
	}
	if err := ValidateRoles(node.Roles); err != nil {
		return fmt.Errorf("node %s: %w", node.NodeId, err)
	}
	if node.Status == NodeStatusRetired || node.Status == NodeStatusTombstoned {
		if len(node.Bindings) != 0 {
			return fmt.Errorf("node %s is %s but still has bindings", node.NodeId, node.Status)
		}
	} else if len(node.Bindings) == 0 {
		return fmt.Errorf("node %s requires a binding", node.NodeId)
	}
	if uint32(len(node.Bindings)) > p.MaxBindings {
		return fmt.Errorf("node %s has %d bindings, max is %d", node.NodeId, len(node.Bindings), p.MaxBindings)
	}
	services := make(map[string]struct{}, len(node.Bindings))
	for _, binding := range node.Bindings {
		if err := ValidateBindingShape(binding); err != nil {
			return fmt.Errorf("node %s: %w", node.NodeId, err)
		}
		if _, ok := services[binding.Service]; ok {
			return fmt.Errorf("node %s duplicate binding service %q", node.NodeId, binding.Service)
		}
		services[binding.Service] = struct{}{}
		key := fmt.Sprintf("%x", binding.Pubkey)
		if _, ok := revoked[key]; ok {
			return fmt.Errorf("node %s pubkey %s: %w", node.NodeId, key, ErrPubkeyReused)
		}
		if owner, ok := live[key]; ok {
			return fmt.Errorf("node %s pubkey %s already bound to %s: %w", node.NodeId, key, owner, ErrPubkeyReused)
		}
		live[key] = node.NodeId
	}
	if err := ValidateEndpoints(node.Endpoints, 0, p.MaxEndpoints); err != nil {
		return fmt.Errorf("node %s: %w", node.NodeId, err)
	}
	if err := ValidateRegion(node.RegionHint); err != nil {
		return fmt.Errorf("node %s: %w", node.NodeId, err)
	}
	if node.Asn != 0 {
		if err := ValidateASN(node.Asn); err != nil {
			return fmt.Errorf("node %s: %w", node.NodeId, err)
		}
	}
	if err := validateBondList(node, p); err != nil {
		return err
	}
	if err := validateGenesisCapacity(node, p); err != nil {
		return err
	}
	if node.DepositBytes == 0 && node.DepositParts != 0 {
		return fmt.Errorf("node %s has deposit parts without a deposit", node.NodeId)
	}
	switch node.Status {
	case NodeStatusRegistered, NodeStatusActive, NodeStatusJailed, NodeStatusRetired, NodeStatusTombstoned:
	default:
		return fmt.Errorf("node %s has status %s", node.NodeId, node.Status)
	}
	if node.Status == NodeStatusRetired || node.Status == NodeStatusTombstoned {
		for _, role := range node.Roles {
			if bondOf(node, role).IsPositive() {
				return fmt.Errorf("node %s is %s but role %s still has a bond", node.NodeId, node.Status, role)
			}
		}
	}
	meets, err := anyRoleMeetsMin(node, p)
	if err != nil {
		return err
	}
	if node.Status == NodeStatusActive && !meets {
		return fmt.Errorf("node %s is active without a role bonded at min_bond", node.NodeId)
	}
	if node.Status == NodeStatusRegistered && meets {
		return fmt.Errorf("node %s is registered but a role is bonded at min_bond", node.NodeId)
	}
	return nil
}

func validateBondList(node Node, p Params) error {
	seen := make(map[Role]struct{}, len(node.Bonds))
	for _, bond := range node.Bonds {
		if !HasRole(node.Roles, bond.Role) {
			return fmt.Errorf("node %s bond for role %s which the node does not have", node.NodeId, bond.Role)
		}
		if _, ok := seen[bond.Role]; ok {
			return fmt.Errorf("node %s has two bonds for %s", node.NodeId, bond.Role)
		}
		seen[bond.Role] = struct{}{}
		if bond.Amount.IsNil() || bond.Amount.IsNegative() {
			return fmt.Errorf("node %s bond for %s must be non-negative", node.NodeId, bond.Role)
		}
	}
	_ = p
	return nil
}

func validateGenesisCapacity(node Node, p Params) error {
	if !HasRole(node.Roles, RoleStorage) {
		if node.DeclaredCapacityBytes != 0 || node.ReservedCapacityBytes != 0 {
			return fmt.Errorf("node %s has capacity without the STORAGE role", node.NodeId)
		}
		return nil
	}
	if node.ReservedCapacityBytes > node.DeclaredCapacityBytes {
		return fmt.Errorf("node %s reserved %d exceeds declared %d", node.NodeId, node.ReservedCapacityBytes, node.DeclaredCapacityBytes)
	}
	backed, err := BackedCapacity(bondOf(node, RoleStorage), p)
	if err != nil {
		return fmt.Errorf("node %s: %w", node.NodeId, err)
	}
	if node.DeclaredCapacityBytes > backed {
		return fmt.Errorf("node %s declared %d exceeds backed %d: %w", node.NodeId, node.DeclaredCapacityBytes, backed, ErrCapacity)
	}
	return nil
}

func validateGenesisCluster(cluster Cluster, p Params, operators map[string]struct{}) error {
	if _, ok := operators[cluster.Operator]; !ok {
		return fmt.Errorf("cluster %s operator %s is not registered", cluster.ClusterId, cluster.Operator)
	}
	if err := validateClusterBody(cluster.ClusterId, cluster.BaseDomain, cluster.PublicEndpoints, cluster.MetadataUri); err != nil {
		return fmt.Errorf("cluster: %w", err)
	}
	if uint32(len(cluster.PublicEndpoints)) > p.MaxEndpoints {
		return fmt.Errorf("cluster %s has %d endpoints, max is %d", cluster.ClusterId, len(cluster.PublicEndpoints), p.MaxEndpoints)
	}
	switch cluster.Status {
	case ClusterStatusActive, ClusterStatusRetired:
	default:
		return fmt.Errorf("cluster %s has status %s", cluster.ClusterId, cluster.Status)
	}
	if cluster.DepositBytes == 0 && cluster.DepositParts != 0 {
		return fmt.Errorf("cluster %s has deposit parts without a deposit", cluster.ClusterId)
	}
	return nil
}

func bondOf(node Node, role Role) math.Int {
	for _, bond := range node.Bonds {
		if bond.Role == role {
			if bond.Amount.IsNil() {
				return math.ZeroInt()
			}
			return bond.Amount
		}
	}
	return math.ZeroInt()
}

func anyRoleMeetsMin(node Node, p Params) (bool, error) {
	for _, role := range node.Roles {
		min, err := p.MinBondFor(role)
		if err != nil {
			return false, err
		}
		if !bondOf(node, role).LT(min) {
			return true, nil
		}
	}
	return false, nil
}
