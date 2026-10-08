package hostfunctions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"github.com/DeBrosOfficial/network/pkg/serverless"
)

// Fetch capabilities (bugboard #266): a function running for a device mints the
// tokens a correspondent downloads one stored object with, without presenting
// an identity (docs/SERVERLESS.md#storage-fetch-capabilities).

var errNoFetchCapIssuer = errors.New("this gateway cannot mint fetch capabilities: it has no cluster secret")

// SetFetchCapIssuer wires what mints fetch capabilities. Called once at gateway
// start, before any function runs.
func (h *HostFunctions) SetFetchCapIssuer(issuer serverless.FetchCapIssuer) {
	h.fetchCapIssuer = issuer
}

// MintStorageFetchCaps issues count fetch capabilities to read cid of the
// calling namespace, issued by the device the caller's session is bound to. It
// returns them as JSON {"namespace","cid","caps":[{"id","token","revoke_key",
// "expires_at"}]}, the body POST /v1/storage/fetch-caps answers.
//
// cid must be in canonical form, as the HTTP mint requires.
//
// Neither ownership nor a storage selector is asked here, and that is the
// authority of a function rather than an omission: the HTTP mint applies them
// because its caller is a credential that may be narrowed (`storage:avatars/*`),
// whereas a function is the namespace's own code, and which of the namespace's
// objects a caller may read is the application's decision, as it is for any
// data a function hands back. A function has no other way to read storage —
// no storage host function is exported to WASM — so this is the one grant of
// read access it can make, and it can only name the namespace's own objects:
// ownership is asked by the gateway that serves a fetch, on every use, so a
// token for a CID the namespace does not own opens nothing. The invocation
// context carries no caller grant to check a selector against. A caller whose session is bound
// to no device cannot mint, because a fetch capability is revoked with the
// device that issued it.
func (h *HostFunctions) MintStorageFetchCaps(ctx context.Context, cid string, count int, ttl time.Duration) (string, error) {
	cur := h.currentInvocationContext(ctx)
	if cur == nil {
		return "", capabilityErr("storage_fetch_cap_mint", errNoInvocation)
	}
	if h.fetchCapIssuer == nil {
		return "", capabilityErr("storage_fetch_cap_mint", errNoFetchCapIssuer)
	}
	if err := ipfs.CanonicalCID(cid); err != nil {
		return "", capabilityErr("storage_fetch_cap_mint", err)
	}
	caps, err := h.fetchCapIssuer.MintFetchCaps(ctx, cur.Namespace, cid, cur.CallerDeviceID, count, ttl)
	if err != nil {
		return "", capabilityErr("storage_fetch_cap_mint", err)
	}
	out, err := json.Marshal(struct {
		Namespace string                `json:"namespace"`
		CID       string                `json:"cid"`
		Caps      []serverless.FetchCap `json:"caps"`
	}{cur.Namespace, cid, caps})
	if err != nil {
		return "", capabilityErr("storage_fetch_cap_mint", fmt.Errorf("encode the fetch capabilities: %w", err))
	}
	return string(out), nil
}
