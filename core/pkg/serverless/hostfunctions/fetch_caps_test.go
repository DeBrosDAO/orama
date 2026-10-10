package hostfunctions

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/serverless"
)

// A canonical CIDv1.
const fetchTestCID = "bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi"

type recordingFetchIssuer struct {
	namespace, cid, device string
	count                  int
	ttl                    time.Duration
	err                    error
}

func (r *recordingFetchIssuer) MintFetchCaps(_ context.Context, namespace, cid, device string, count int, ttl time.Duration) ([]serverless.FetchCap, error) {
	r.namespace, r.cid, r.device, r.count, r.ttl = namespace, cid, device, count, ttl
	if r.err != nil {
		return nil, r.err
	}
	caps := make([]serverless.FetchCap, count)
	for i := range caps {
		caps[i] = serverless.FetchCap{ID: strings.Repeat("a", 31) + string(rune('0'+i)), Token: "token", RevokeKey: "revoke", ExpiresAt: 42}
	}
	return caps, nil
}

func TestMintStorageFetchCaps_mintsForTheCallersNamespaceAndDevice(t *testing.T) {
	issuer := &recordingFetchIssuer{}
	h := &HostFunctions{}
	h.SetFetchCapIssuer(issuer)
	ctx := invocationCtx(&serverless.InvocationContext{
		Namespace: "anchat", FunctionName: "rpc-router", CallerDeviceID: "device-1",
	})

	out, err := h.MintStorageFetchCaps(ctx, fetchTestCID, 3, 2*time.Hour)
	if err != nil {
		t.Fatalf("MintStorageFetchCaps: %v", err)
	}
	if issuer.namespace != "anchat" || issuer.cid != fetchTestCID || issuer.device != "device-1" ||
		issuer.count != 3 || issuer.ttl != 2*time.Hour {
		t.Errorf("minted with %+v", issuer)
	}
	var got struct {
		Namespace string                `json:"namespace"`
		CID       string                `json:"cid"`
		Caps      []serverless.FetchCap `json:"caps"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("the result is not JSON: %v (%s)", err, out)
	}
	if got.Namespace != "anchat" || got.CID != fetchTestCID || len(got.Caps) != 3 || got.Caps[0].Token != "token" || got.Caps[0].RevokeKey != "revoke" {
		t.Errorf("MintStorageFetchCaps returned %s", out)
	}
}

func TestMintStorageFetchCaps_refusedWithoutAnIssuerAnInvocationOrAnIssuerSayingNo(t *testing.T) {
	h := &HostFunctions{}
	ctx := invocationCtx(&serverless.InvocationContext{Namespace: "anchat", FunctionName: "fn", CallerDeviceID: "d"})
	if _, err := h.MintStorageFetchCaps(ctx, fetchTestCID, 1, time.Hour); err == nil {
		t.Error("minted with no issuer wired")
	}
	h.SetFetchCapIssuer(&recordingFetchIssuer{})
	if _, err := h.MintStorageFetchCaps(context.Background(), fetchTestCID, 1, time.Hour); err == nil {
		t.Error("minted outside an invocation")
	}
	h.SetFetchCapIssuer(&recordingFetchIssuer{err: errors.New("no device")})
	if _, err := h.MintStorageFetchCaps(ctx, fetchTestCID, 1, time.Hour); err == nil {
		t.Error("the issuer's refusal was not passed on")
	}
}

func TestMintStorageFetchCaps_refusesANonCanonicalCID(t *testing.T) {
	issuer := &recordingFetchIssuer{}
	h := &HostFunctions{}
	h.SetFetchCapIssuer(issuer)
	ctx := invocationCtx(&serverless.InvocationContext{Namespace: "anchat", FunctionName: "fn", CallerDeviceID: "d"})
	for name, cid := range map[string]string{
		"empty":      "",
		"not a cid":  "bafyexample",
		"path":       "/ipfs/" + fetchTestCID,
		"upper case": strings.ToUpper(fetchTestCID),
		"newline":    fetchTestCID + "\n",
	} {
		if _, err := h.MintStorageFetchCaps(ctx, cid, 1, time.Hour); err == nil {
			t.Errorf("%s: %q was minted", name, cid)
		}
	}
	if issuer.count != 0 {
		t.Errorf("the issuer was reached %d times with a bad CID", issuer.count)
	}
}
