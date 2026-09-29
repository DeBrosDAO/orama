package types

import "errors"

var (
	// ErrNotFound is returned when an operator, node, cluster, or binding is absent.
	ErrNotFound = errors.New("not found")
	// ErrNotActive is returned when an operation needs a node that can act (not jailed, retired or
	// tombstoned, and holding the role) and the node cannot.
	ErrNotActive = errors.New("node is not active for this operation")
	// ErrExists is returned when a registration reuses an id.
	ErrExists = errors.New("already exists")
	// ErrUnauthorized is returned when the signer is not the record's operator.
	ErrUnauthorized = errors.New("signer is not the operator")
	// ErrInvalidBinding is returned when a service-key signature does not verify.
	ErrInvalidBinding = errors.New("binding signature is invalid")
	// ErrPubkeyReused is returned when a service pubkey is live, retired, or tombstoned.
	ErrPubkeyReused = errors.New("service pubkey cannot be reused")
	// ErrHotKey is returned when a hot key equals its operator.
	ErrHotKey = errors.New("hot key must differ from the operator")
	// ErrEndpointTaken is returned when a literal-IP endpoint is already registered by another node.
	ErrEndpointTaken = errors.New("endpoint address is registered by another node")
	// ErrCapacity is returned when declared capacity is above the bond-backed cap.
	ErrCapacity = errors.New("declared capacity exceeds backed capacity")
	// ErrReserved is returned when a capacity change would drop below what is reserved.
	ErrReserved = errors.New("reserved capacity exceeds the declaration")
	// ErrUnbondingRejected marks a matured unbonding entry that cannot be paid (an unusable
	// recipient, a bank that refuses the transfer). The entry stays queued and is retried in the next
	// block. Any other failure of the payment (a collection that cannot be read or decoded) fails the
	// block.
	ErrUnbondingRejected = errors.New("unbonding cannot be paid")
)
