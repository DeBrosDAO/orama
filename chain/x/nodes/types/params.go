package types

import (
	"fmt"

	"cosmossdk.io/math"

	"github.com/DeBrosOfficial/network/chain/app/params"
)

const (
	// DefaultUnbondingSeconds is the genesis unbonding queue delay: 21 days.
	// plans/open-network.md D16 and track-c-chain.md C4 state a 21-day unbonding
	// period. C6 puts role-bond releases on a queue and does not give a second
	// duration, so the role-bond queue uses the same 21 days.
	DefaultUnbondingSeconds int64 = 21 * 24 * 60 * 60

	// DefaultProbationCapacityBytes is the STORAGE capacity a node may declare
	// with a zero STORAGE bond. C2 calls this "a small capped capacity" and
	// does not fix the byte count. 1 GiB until G1 signs a number.
	DefaultProbationCapacityBytes uint64 = 1 << 30

	// DefaultMinServiceVolumeBytes is the declared STORAGE capacity a service
	// day must reach. C5 / G4 require "proven service above a minimum volume"
	// and do not fix the byte count. 1 byte means any positive declaration
	// counts; an active RELAY role counts without it.
	DefaultMinServiceVolumeBytes uint64 = 1

	// DefaultNetworkIdentityLockSeconds is how long a node's declared ASN and
	// derived /16 must stand unchanged before protocol-deal slots and the
	// operator house count them: 14 days. The spec asks for "harder to game" and
	// gives no number; 14 days matches the parameter-change timelock (D17), so
	// moving identity to fit a vote costs longer than the vote can be held open.
	DefaultNetworkIdentityLockSeconds int64 = 14 * 24 * 60 * 60

	// DefaultMaxEndpoints and DefaultMaxBindings bound a single record so one
	// registration cannot write an unbounded list.
	DefaultMaxEndpoints uint32 = 8
	DefaultMaxBindings  uint32 = 8

	// absoluteEndpointCap and absoluteBindingCap are the largest values
	// Params.Validate accepts. They are not the genesis defaults.
	absoluteEndpointCap uint32 = 64
	absoluteBindingCap  uint32 = 64
)

// DefaultMinRoleBond is the genesis per-role minimum bond: 1 ORAMA.
// plans/open-network/track-g-token-legal-security.md G1 lists min_bond[role]
// as still to be sized ("a fake identity costs more than its expected
// reward"). C6 requires a positive floor and states no amount.
var DefaultMinRoleBond = math.NewInt(params.NoramaPerOrama)

// DefaultBondPerGiB is the genesis STORAGE bond per GiB of declared
// capacity: 1 ORAMA. G1 lists bond_per_gib as unset. C6 caps declared
// capacity by bond / bond_per_gib.
var DefaultBondPerGiB = math.NewInt(params.NoramaPerOrama)

// DefaultDepositPerByte is the genesis state-deposit price, in norama per
// byte. plans/open-network.md P3 sizes it so filling the 30 GB state budget
// locks about 10% of year-10 supply, stated as ≈0.07 ORAMA/KiB.
// 68359 norama * 1024 = 69_999_616 norama ≈ 0.07 ORAMA per KiB. G1 has not
// signed the final integer.
var DefaultDepositPerByte = math.NewInt(68_359)

// AllRoles is every role a node may register, in enum order.
func AllRoles() []Role {
	return []Role{RoleValidator, RoleStorage, RoleRelay, RoleExit, RoleDirauth, RoleArchiver}
}

// NewParams builds a Params from its fields, applying no defaults.
func NewParams(
	minBond []RoleBond,
	bondPerGiB math.Int,
	unbondingSeconds int64,
	depositPerByte math.Int,
	probationCapacityBytes uint64,
	minServiceVolumeBytes uint64,
	maxEndpoints, maxBindings uint32,
	networkIdentityLockSeconds int64,
) Params {
	return Params{
		MinBond:                minBond,
		BondPerGib:             bondPerGiB,
		UnbondingSeconds:       unbondingSeconds,
		DepositPerByte:         depositPerByte,
		ProbationCapacityBytes: probationCapacityBytes,
		MinServiceVolumeBytes:  minServiceVolumeBytes,
		MaxEndpoints:           maxEndpoints,
		MaxBindings:            maxBindings,

		NetworkIdentityLockSeconds: networkIdentityLockSeconds,
	}
}

// DefaultParams returns x/nodes' genesis parameters. See the Default*
// constants for which numbers the spec fixes and which are placeholders.
func DefaultParams() Params {
	minBond := make([]RoleBond, 0, len(AllRoles()))
	for _, role := range AllRoles() {
		minBond = append(minBond, RoleBond{Role: role, Amount: DefaultMinRoleBond})
	}
	return NewParams(
		minBond,
		DefaultBondPerGiB,
		DefaultUnbondingSeconds,
		DefaultDepositPerByte,
		DefaultProbationCapacityBytes,
		DefaultMinServiceVolumeBytes,
		DefaultMaxEndpoints,
		DefaultMaxBindings,
		DefaultNetworkIdentityLockSeconds,
	)
}

// MinBond returns the positive minimum bond configured for role.
func (p Params) MinBondFor(role Role) (math.Int, error) {
	for _, b := range p.MinBond {
		if b.Role == role {
			if b.Amount.IsNil() || !b.Amount.IsPositive() {
				return math.Int{}, fmt.Errorf("min_bond for %s is not positive", role)
			}
			return b.Amount, nil
		}
	}
	return math.Int{}, fmt.Errorf("min_bond missing for %s", role)
}

// Validate checks Params for internal consistency.
func (p Params) Validate() error {
	seen := make(map[Role]struct{}, len(p.MinBond))
	if len(p.MinBond) != len(AllRoles()) {
		return fmt.Errorf("min_bond must list each role once, got %d entries", len(p.MinBond))
	}
	for _, b := range p.MinBond {
		if !knownRole(b.Role) {
			return fmt.Errorf("min_bond has unknown role %s", b.Role)
		}
		if _, ok := seen[b.Role]; ok {
			return fmt.Errorf("min_bond lists %s twice", b.Role)
		}
		seen[b.Role] = struct{}{}
		if b.Amount.IsNil() || !b.Amount.IsPositive() {
			return fmt.Errorf("min_bond for %s must be positive", b.Role)
		}
	}
	for _, role := range AllRoles() {
		if _, ok := seen[role]; !ok {
			return fmt.Errorf("min_bond missing for %s", role)
		}
	}
	if p.BondPerGib.IsNil() || !p.BondPerGib.IsPositive() {
		return fmt.Errorf("bond_per_gib must be a positive integer")
	}
	if p.UnbondingSeconds <= 0 {
		return fmt.Errorf("unbonding_seconds must be positive, got %d", p.UnbondingSeconds)
	}
	if p.DepositPerByte.IsNil() || !p.DepositPerByte.IsPositive() {
		return fmt.Errorf("deposit_per_byte must be a positive integer")
	}
	if p.MaxEndpoints == 0 || p.MaxEndpoints > absoluteEndpointCap {
		return fmt.Errorf("max_endpoints must be in [1, %d], got %d", absoluteEndpointCap, p.MaxEndpoints)
	}
	if p.NetworkIdentityLockSeconds < 0 {
		return fmt.Errorf("network_identity_lock_seconds must not be negative, got %d", p.NetworkIdentityLockSeconds)
	}
	if p.MaxBindings == 0 || p.MaxBindings > absoluteBindingCap {
		return fmt.Errorf("max_bindings must be in [1, %d], got %d", absoluteBindingCap, p.MaxBindings)
	}
	return nil
}

func knownRole(role Role) bool {
	switch role {
	case RoleValidator, RoleStorage, RoleRelay, RoleExit, RoleDirauth, RoleArchiver:
		return true
	default:
		return false
	}
}
