//go:build e2e_fleet

package relayedfetch

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
)

// TestRelayedFetch_throughTheRelayMatchesGet: three capabilities, three
// fetches through a relay that is given no credential; the bytes and the
// headers are those of /v1/storage/get, with a Content-Length
// (docs/whitepaper/technical-reference/appendices/i-api-surface.md#storage).
func TestRelayedFetch_throughTheRelayMatchesGet(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	caps := fx.mint(t, fx.cid, 3)
	direct := fx.get(t, fx.cid)

	for i, c := range caps {
		eventually.Require(t, pollEvery, torBudget, fmt.Sprintf("fetch %d through the relay", i+1), func() (bool, error) {
			resp, body, err := relayedFetch(t, fx.relay, fx.host, fx.cid, c.Token)
			if err != nil {
				return false, err
			}
			if resp.StatusCode != http.StatusOK {
				return false, eventually.Stop(fmt.Errorf("status %d: %.200s", resp.StatusCode, body))
			}
			if !bytes.Equal(body, fx.content) {
				return false, eventually.Stop(fmt.Errorf("fetched %d bytes that differ from the %d uploaded", len(body), len(fx.content)))
			}
			for _, h := range []string{"Content-Type", "Content-Disposition", "Cache-Control"} {
				if got, want := resp.Header.Get(h), direct.Header.Get(h); got != want {
					return false, eventually.Stop(fmt.Errorf("%s = %q through the relay, %q from get", h, got, want))
				}
			}
			if resp.ContentLength != int64(len(fx.content)) {
				return false, eventually.Stop(fmt.Errorf("Content-Length = %d, want %d", resp.ContentLength, len(fx.content)))
			}
			return true, nil
		})
	}
}

// TestRelayedDownload_refusals: the refusal matrix against S directly, which
// costs no circuit: no header, a capability for another CID, forged, the
// credential beside it. Forged and wrong-CID are one code.
func TestRelayedDownload_refusals(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	other := fx.upload(t, append([]byte("second object "), fx.content[:1024]...))
	caps := fx.mint(t, fx.cid, 1)

	tenancy.ExpectRefused(t, fx.direct(t, fx.cid, "", nil), http.StatusUnauthorized, codeMissing)
	tenancy.ExpectRefused(t, fx.direct(t, other, caps[0].Token, nil), http.StatusForbidden, codeInvalid)
	tenancy.ExpectRefused(t, fx.direct(t, fx.cid, caps[0].Token[:len(caps[0].Token)-4]+"AAAA", nil), http.StatusForbidden, codeInvalid)
	tenancy.ExpectRefused(t, fx.direct(t, fx.cid, "not-a-capability", nil), http.StatusForbidden, codeInvalid)
	beside := http.Header{"Authorization": {"Bearer " + fx.token()}}
	tenancy.ExpectRefused(t, fx.direct(t, fx.cid, caps[0].Token, beside), http.StatusBadRequest, codeNotAlone)

	good := fx.direct(t, fx.cid, caps[0].Token, nil).Expect(t, http.StatusOK)
	if !bytes.Equal(good.Body, fx.content) {
		t.Errorf("a direct relayed download returned %d bytes that differ from the %d uploaded", len(good.Body), len(fx.content))
	}
}

// TestRelayedDownload_revokedCapabilityIsRefused: DELETE by id, with the
// revoke key the mint returned for it, refuses that capability within the
// revocation list's staleness and no other. An id with no key, or with another
// capability's key, revokes nothing.
func TestRelayedDownload_revokedCapabilityIsRefused(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	caps := fx.mint(t, fx.cid, 2)
	fx.direct(t, fx.cid, caps[0].Token, nil).Expect(t, http.StatusOK)

	tenancy.ExpectRefused(t, fx.revoke(t, caps[0].ID, ""), http.StatusForbidden, codeRevokeKey)
	tenancy.ExpectRefused(t, fx.revoke(t, caps[0].ID, caps[1].RevokeKey), http.StatusForbidden, codeRevokeKey)
	tenancy.ExpectRefused(t, fx.revoke(t, "0123456789abcdef0123456789abcdef", caps[0].RevokeKey), http.StatusForbidden, codeRevokeKey)
	fx.direct(t, fx.cid, caps[0].Token, nil).Expect(t, http.StatusOK)

	r := fx.revoke(t, caps[0].ID, caps[0].RevokeKey).Expect(t, http.StatusOK)
	if !strings.Contains(string(r.Body), caps[0].ID) {
		t.Errorf("revoke answered %s, want the id back", r.Body)
	}
	fx.revoke(t, caps[0].ID, caps[0].RevokeKey).Expect(t, http.StatusOK) // idempotent
	eventually.Require(t, time.Second, revocationBound, "the revoked capability refused", func() (bool, error) {
		resp := fx.direct(t, fx.cid, caps[0].Token, nil)
		if resp.Status == http.StatusOK {
			return false, fmt.Errorf("still served")
		}
		if resp.Status != http.StatusForbidden || resp.ErrorCode() != codeRevoked {
			return false, eventually.Stop(fmt.Errorf("want 403 %s, got %d %.200s", codeRevoked, resp.Status, resp.Body))
		}
		return true, nil
	})
	fx.direct(t, fx.cid, caps[1].Token, nil).Expect(t, http.StatusOK)

	fx.revoke(t, "not-an-id", caps[0].RevokeKey).Expect(t, http.StatusBadRequest)
}

// TestFetchCapMint_boundsAndDeviceRule: count 1 to 64, ttl 3600 to 604800, a
// CID the namespace owns, and a device-bound session.
func TestFetchCapMint_boundsAndDeviceRule(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	owner := fx.token()
	for name, body := range map[string]map[string]any{
		"count zero":        {"cid": fx.cid, "count": 0, "ttl_seconds": 3600},
		"count 65":          {"cid": fx.cid, "count": 65, "ttl_seconds": 3600},
		"ttl under an hour": {"cid": fx.cid, "count": 1, "ttl_seconds": 3599},
		"ttl over a week":   {"cid": fx.cid, "count": 1, "ttl_seconds": 604801},
		"not a CID":         {"cid": "nope", "count": 1, "ttl_seconds": 3600},
	} {
		if r := fx.mintRaw(t, owner, body); r.Status != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d %.200s", name, r.Status, r.Body)
		}
	}
	if r := fx.mintRaw(t, owner, map[string]any{"cid": "bafkreigh2akiscaildcqabsyg3dfr6chu3fgpregiymsck7e7aqa4s52zy", "count": 1, "ttl_seconds": 3600}); r.Status != http.StatusForbidden {
		t.Errorf("a CID the namespace does not own: want 403, got %d %.200s", r.Status, r.Body)
	}
	fx.mint(t, fx.cid, 64)

	// A member's session bound to no device cannot mint.
	member := tenancy.Member(t, fx.n, tenancy.RoleRuntime)
	tenancy.ExpectRefused(t,
		fx.mintRaw(t, member.Token(), map[string]any{"cid": fx.cid, "count": 1, "ttl_seconds": 3600}),
		http.StatusForbidden, codeDeviceRequired)
}

// TestRelay_refusesDestinationsAndNeedsNoCredential: the relay is anonymous
// and pinned: a host off its allowlist, port 80 and an IP literal are
// 400 RELAY_DESTINATION_NOT_ALLOWED with a code; an allowlisted host upgrades
// with no credential.
func TestRelay_refusesDestinationsAndNeedsNoCredential(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	r := harness.GW(t)
	nsHost := tenancy.NamespaceHost(f, "anything")
	for name, target := range map[string][2]string{
		"a public site": {"example.com", "443"},
		"another port":  {nsHost, "80"},
		"an IP literal": {"1.1.1.1", "443"},
		"loopback":      {"127.0.0.1", "443"},
		// A name that only ends like the cluster's domain, not a subdomain of
		// it: every name under the base domain is the cluster's own and allowed.
		"a lookalike":    {"evil" + f.State.BaseDomain, "443"},
		"a suffix trick": {nsHost + ".example.com", "443"},
		"a kelvin sign":  {"\u212A" + nsHost, "443"}, // lowercases to an ASCII "k" under the allowed suffix
	} {
		ws, refused := openRelay(t, r, target[0], target[1])
		if ws != nil {
			t.Errorf("%s: the relay upgraded", name)
			continue
		}
		tenancy.ExpectRefused(t, refused, http.StatusBadRequest, codeNotAllowed)
	}
	if ws, refused := openRelay(t, r, nsHost, "443"); ws == nil {
		t.Errorf("an allowlisted host was refused: %d %.200s", refused.Status, refused.Body)
	}
}

// TestRelayedFetch_namespaceRequestLogsKeepNoAddress: after relayed downloads
// the namespace's request_logs hold rows for the route and none carries an
// address (docs/whitepaper/technical-reference/vol1/36-sdks.md#relayed-fetch). The relay's own row is in the
// cluster gateway's database, which a tenant cannot read; the unit tests pin
// its empty ip.
func TestRelayedFetch_namespaceRequestLogsKeepNoAddress(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	caps := fx.mint(t, fx.cid, 2)
	for _, c := range caps {
		fx.direct(t, fx.cid, c.Token, nil).Expect(t, http.StatusOK)
	}
	query := map[string]string{"sql": "SELECT COUNT(*) AS n, SUM(CASE WHEN ip = '' THEN 1 ELSE 0 END) AS blank " +
		"FROM request_logs WHERE path LIKE '/v1/storage/relayed/%'"}
	eventually.Require(t, pollEvery, logFlush, "request_logs rows for the relayed route", func() (bool, error) {
		r := tenancy.Post(t, fx.c, pathQuery, tenancy.Cred{Bearer: fx.token()}, query)
		if r.Status != http.StatusOK {
			return false, eventually.Stop(fmt.Errorf("query answered %d %.200s", r.Status, r.Body))
		}
		var out struct {
			Items []struct {
				N     float64 `json:"n"`
				Blank float64 `json:"blank"`
			} `json:"items"`
		}
		if err := r.Decode(&out); err != nil || len(out.Items) != 1 {
			return false, eventually.Stop(fmt.Errorf("query answered %s (%v)", r.Body, err))
		}
		row := out.Items[0]
		if row.N == 0 {
			return false, fmt.Errorf("no row logged yet")
		}
		if row.N != row.Blank {
			return false, eventually.Stop(fmt.Errorf("%v of %v rows for the relayed route carry an address", row.N-row.Blank, row.N))
		}
		return true, nil
	})
}
