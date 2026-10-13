// Package releasepub is the maintainer's side of the release repository: it
// builds and signs the TUF metadata a client (pkg/releaseverify) reads, and
// publishes it.
//
// A repository directory holds the metadata a web host serves:
//
//	root.json  <N>.root.json  targets.json  snapshot.json  timestamp.json
//
// The archives are not in it: they are GitHub release assets, and the host
// answers a request for targets/<path> with a redirect to the asset.
//
// Every signature is made by the RootWallet agent under the purpose
// orama-release (pkg/releasesign), with the one release key the wallet holds.
// That key is listed in the root for all four top-level roles at threshold 1.
// The agent shows a person the metadata and signs only if they approve, once
// per signature, and it enforces rules this package checks first
// (CheckSignable) so that a request it would refuse is never offered:
// canonical JSON, at most 256 KiB, at most 200 targets, no delegations. So a
// release channel is the path prefix of its targets (nightly/..., main/...,
// dev/<branch>/...), not a role.
//
// A release is three approvals (targets, snapshot, timestamp). Refreshing the
// timestamp is one.
package releasepub

import (
	"context"
	"crypto/ed25519"
	"time"

	"github.com/DeBrosOfficial/network/pkg/releasesign"
)

// Agent is the part of the RootWallet agent a release is made with.
// *rwagent.Client is one.
type Agent interface {
	releasesign.Signer
	// ReleaseKey is the wallet's release public key.
	ReleaseKey(ctx context.Context) (ed25519.PublicKey, error)
}

// Validity periods and bounds. A role's metadata is never valid past the root's
// own expiry.
const (
	// RootValidity is how long a root is valid from the day it is made or
	// renewed.
	RootValidity = 365 * 24 * time.Hour
	// RolesValidity is how long targets and snapshot are valid from the day
	// they are signed. They are re-signed with every release; only the
	// timestamp is refreshed on a schedule, so these must outlive a quiet
	// period.
	RolesValidity = 365 * 24 * time.Hour
	// ShortTimestampValidity is the timestamp's validity after a nightly or
	// dev release or refresh; MainTimestampValidity after a main one. A
	// repository stops being believed this long after its timestamp was last
	// signed (freeze protection).
	ShortTimestampValidity = 7 * 24 * time.Hour
	MainTimestampValidity  = 30 * 24 * time.Hour

	// MaxTargets is how many targets one targets file may list: the RootWallet
	// agent refuses to sign a targets payload with more.
	MaxTargets = 200
	// DefaultRetention is how many versions of a channel stay listed.
	DefaultRetention = 3
)
