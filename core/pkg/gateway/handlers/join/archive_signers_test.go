package join

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/archivetrust"
	"github.com/DeBrosOfficial/network/pkg/constants"
)

const testArchiveSigner = "0x1111111111111111111111111111111111111111"

// useArchiveSigners makes this node's trust anchor read as signers, or fail
// with err.
func useArchiveSigners(t *testing.T, signers []string, err error) {
	t.Helper()
	prev := readArchiveSigners
	t.Cleanup(func() { readArchiveSigners = prev })
	readArchiveSigners = func() ([]string, string, error) { return signers, "", err }
}

func TestHandleJoin_responseCarriesTheArchiveSigners(t *testing.T) {
	c := &claimQuery{}
	h := joinableHandler(t, c)
	signers := []string{testArchiveSigner, "0x2222222222222222222222222222222222222222"}
	useArchiveSigners(t, signers, nil)

	rec := httptest.NewRecorder()
	h.HandleJoin(rec, httptest.NewRequest(http.MethodPost, "/v1/internal/join",
		bytes.NewReader(joinBody(t, "203.0.113.9"))))

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	var resp JoinResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !slices.Equal(resp.ArchiveSigners, signers) {
		t.Fatalf("archive_signers = %v, want this node's anchor %v", resp.ArchiveSigners, signers)
	}
}

func TestHandleJoin_withoutATrustAnchorRefusesBeforeSpendingTheToken(t *testing.T) {
	c := &claimQuery{}
	h := joinableHandler(t, c)
	useArchiveSigners(t, nil, fmt.Errorf("%w: missing", archivetrust.ErrNoAnchor))

	rec := httptest.NewRecorder()
	h.HandleJoin(rec, httptest.NewRequest(http.MethodPost, "/v1/internal/join",
		bytes.NewReader(joinBody(t, "203.0.113.9"))))

	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), archivetrust.AnchorPath) {
		t.Fatalf("got %d %q, want a 500 naming the anchor", rec.Code, rec.Body.String())
	}
	for _, stmt := range c.execs {
		if strings.Contains(stmt, "invite_tokens") {
			t.Fatalf("the invite was spent on a join this node could not complete: %q", stmt)
		}
	}
}

func TestHandleJoin_unreadableTrustAnchorIsRefusedToo(t *testing.T) {
	h := joinableHandler(t, &claimQuery{})
	useArchiveSigners(t, nil, errors.New("owned by 999:999, not 0:0"))

	rec := httptest.NewRecorder()
	h.HandleJoin(rec, httptest.NewRequest(http.MethodPost, "/v1/internal/join",
		bytes.NewReader(joinBody(t, "203.0.113.9"))))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("got %d, want 500", rec.Code)
	}
}

func TestPeerAddresses_useThePortConstants(t *testing.T) {
	const wgIP, id = "10.0.0.7", "12D3KooWTestPeer"
	if got, want := ipfsClusterPeer(wgIP, id).Addrs, fmt.Sprintf("/ip4/10.0.0.7/tcp/%d/p2p/%s", constants.IPFSClusterSwarmPort, id); !slices.Equal(got, []string{want}) {
		t.Errorf("IPFS Cluster peer = %v, want [%s] (the swarm listens on IPFSClusterSwarmPort)", got, want)
	}
	if got, want := ipfsPeer(wgIP, id).Addrs, fmt.Sprintf("/ip4/10.0.0.7/tcp/%d/p2p/%s", constants.IPFSSwarmPort, id); !slices.Equal(got, []string{want}) {
		t.Errorf("IPFS peer = %v, want [%s]", got, want)
	}
	peers := []WGPeerInfo{{AllowedIP: "10.0.0.2/32"}}
	want := []string{
		fmt.Sprintf("10.0.0.2:%d", constants.OlricMemberlistPort),
		fmt.Sprintf("10.0.0.7:%d", constants.OlricMemberlistPort),
	}
	if got := olricSeedPeers(peers, wgIP); !slices.Equal(got, want) {
		t.Errorf("Olric seeds = %v, want %v (the index memberlist port)", got, want)
	}
}

func TestHandleJoin_responseCarriesTheRotationMark(t *testing.T) {
	h := joinableHandler(t, &claimQuery{})
	prev := readArchiveSigners
	t.Cleanup(func() { readArchiveSigners = prev })
	readArchiveSigners = func() ([]string, string, error) {
		return []string{testArchiveSigner}, "2026-09-20T10:00:00Z", nil
	}

	rec := httptest.NewRecorder()
	h.HandleJoin(rec, httptest.NewRequest(http.MethodPost, "/v1/internal/join",
		bytes.NewReader(joinBody(t, "203.0.113.9"))))
	var resp JoinResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode (%d): %v", rec.Code, err)
	}
	if resp.ArchiveSignersRotatedAt != "2026-09-20T10:00:00Z" {
		t.Fatalf("archive_signers_rotated_at = %q", resp.ArchiveSignersRotatedAt)
	}
}

func joinBodyExpecting(t *testing.T, expected []string) []byte {
	t.Helper()
	var req JoinRequest
	if err := json.Unmarshal(joinBody(t, "203.0.113.9"), &req); err != nil {
		t.Fatal(err)
	}
	req.ExpectedArchiveSigners = expected
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// The joiner refuses a response naming signers it did not expect, after the
// invite is spent and its peer row written. The handler refuses first.
func TestHandleJoin_anUnexpectedSignerListIsRefusedBeforeAnythingIsSpent(t *testing.T) {
	c := &claimQuery{}
	h := joinableHandler(t, c)
	rec := httptest.NewRecorder()
	h.HandleJoin(rec, httptest.NewRequest(http.MethodPost, "/v1/internal/join",
		bytes.NewReader(joinBodyExpecting(t, []string{"0x2222222222222222222222222222222222222222"}))))

	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), testArchiveSigner) {
		t.Fatalf("got %d %q, want a 409 naming the cluster's signers", rec.Code, rec.Body.String())
	}
	for _, stmt := range c.execs {
		if strings.Contains(stmt, "invite_tokens") || strings.Contains(stmt, "wireguard_peers") {
			t.Fatalf("a refused join still changed cluster state: %q", stmt)
		}
	}
}

func TestHandleJoin_theExpectedSignerListIsAdmitted(t *testing.T) {
	h := joinableHandler(t, &claimQuery{})
	rec := httptest.NewRecorder()
	h.HandleJoin(rec, httptest.NewRequest(http.MethodPost, "/v1/internal/join",
		bytes.NewReader(joinBodyExpecting(t, []string{" " + testArchiveSigner}))))
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
}

func TestHandleJoin_aMalformedExpectationIsABadRequest(t *testing.T) {
	c := &claimQuery{}
	h := joinableHandler(t, c)
	rec := httptest.NewRecorder()
	h.HandleJoin(rec, httptest.NewRequest(http.MethodPost, "/v1/internal/join",
		bytes.NewReader(joinBodyExpecting(t, []string{"0x12"}))))
	if rec.Code != http.StatusBadRequest || len(c.execs) != 0 {
		t.Fatalf("got %d, statements %v", rec.Code, c.execs)
	}
}

// Anyone can send a join body; a huge expectation list must be refused on its
// length alone, before the invite is looked up and before any per-entry work.
func TestHandleJoin_anOversizedExpectationIsRefusedBeforeTheTokenQuery(t *testing.T) {
	c := &claimQuery{}
	h := joinableHandler(t, c)
	many := make([]string, archivetrust.MaxSigners+1)
	for i := range many {
		many[i] = fmt.Sprintf("0x%040x", i+1)
	}
	rec := httptest.NewRecorder()
	h.HandleJoin(rec, httptest.NewRequest(http.MethodPost, "/v1/internal/join", bytes.NewReader(joinBodyExpecting(t, many))))
	if rec.Code != http.StatusBadRequest || c.tokenQueries != 0 || len(c.execs) != 0 {
		t.Fatalf("got %d; token queries %d; statements %v", rec.Code, c.tokenQueries, c.execs)
	}
}
