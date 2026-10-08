package storage

import (
	"errors"
	"net/http"
	"strings"
	"sync"

	gwauth "github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/capability"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/httputil"
	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
)

// Relayed download (bugboard #266): GET /v1/storage/relayed/{cid} with a fetch
// capability and no credential. The client reaches this route through a relay
// node's tunnel, so the address it arrives from is a Tor exit, and what it
// presents names no account.
//
// The route answers exactly as /v1/storage/get/{cid} does for the same object.
// It differs only in who it believes: the capability, not a principal.

const (
	// RelayedPathPrefix is the route's path; the CID follows it.
	RelayedPathPrefix = "/v1/storage/relayed/"

	// FetchCapHeader carries the capability.
	FetchCapHeader = "X-Orama-Fetch-Cap"

	// maxConcurrentPerFetchCap bounds the downloads one capability holds open on
	// a gateway at once. A capability is a bearer token handed to a correspondent;
	// without a bound one leaked token could pin a gateway's reads.
	maxConcurrentPerFetchCap = 4

	fetchCapInvalidMessage = "the fetch capability is not valid for this content"
)

// fetchCounter counts the downloads each capability has in flight.
type fetchCounter struct {
	mu   sync.Mutex
	open map[string]int
}

func newFetchCounter() *fetchCounter { return &fetchCounter{open: map[string]int{}} }

func (c *fetchCounter) acquire(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.open[id] >= maxConcurrentPerFetchCap {
		return false
	}
	c.open[id]++
	return true
}

func (c *fetchCounter) release(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.open[id] <= 1 {
		delete(c.open, id)
		return
	}
	c.open[id]--
}

// RelayedDownloadHandler handles GET /v1/storage/relayed/:cid.
//
// The capability is checked before anything is read: one HMAC for a stranger,
// no registry query and no IPFS call. A forged, expired, wrong-CID,
// wrong-namespace or wrong-kind token is one refusal; a revoked one says so.
func (h *Handlers) RelayedDownloadHandler(w http.ResponseWriter, r *http.Request) {
	if !httputil.CheckMethod(w, r, http.MethodGet) {
		return
	}
	token := strings.TrimSpace(r.Header.Get(FetchCapHeader))
	if token == "" {
		writeFetchCapError(w, http.StatusUnauthorized, CodeFetchCapMissing,
			"a relayed download carries a fetch capability in the "+FetchCapHeader+" header")
		return
	}
	cid := strings.TrimPrefix(r.URL.Path, RelayedPathPrefix)
	if err := ipfs.CanonicalCID(cid); err != nil {
		httputil.WriteRPCError(w, http.StatusBadRequest, httputil.ErrCodeValidationFailed,
			"the path names a CID that does not parse or is not in canonical form")
		return
	}
	if presentsCredential(r) {
		writeFetchCapError(w, http.StatusBadRequest, CodeFetchCapNotAlone,
			"a fetch capability is presented alone: this request also carries a credential")
		return
	}
	namespace, claims, ok := h.checkFetchCap(w, token, cid)
	if !ok {
		return
	}
	if !h.fetchStreams.acquire(claims.ID) {
		httputil.WriteRPCError(w, http.StatusTooManyRequests, httputil.ErrCodeRateLimited,
			"too many downloads are open on this capability", httputil.WithRetryable())
		return
	}
	defer h.fetchStreams.release(claims.ID)
	// Only now does the request reach anything the capability protects.
	if h.ipfsClient == nil {
		httputil.WriteRPCError(w, http.StatusServiceUnavailable, httputil.ErrCodeServiceUnavailable, "IPFS storage not available")
		return
	}
	if !h.requireOwnedCID(w, r, cid, namespace) {
		return
	}
	h.serveStored(w, r, cid, namespace)
}

// checkFetchCap verifies the capability against the namespace this gateway
// serves and the CID in the path. It writes the refusal itself.
func (h *Handlers) checkFetchCap(w http.ResponseWriter, token, cid string) (string, *capability.FetchClaims, bool) {
	if h.fetchCaps == nil {
		writeFetchCapError(w, http.StatusServiceUnavailable, CodeFetchCapUnavailable,
			"this gateway cannot check fetch capabilities: it has no cluster secret")
		return "", nil, false
	}
	namespace := strings.TrimSpace(h.config.ServedNamespace)
	if namespace == "" {
		// The index gateway serves no namespace of its own; a capability names
		// one, and the host that names it is served by that namespace's gateway.
		writeFetchCapError(w, http.StatusForbidden, CodeFetchCapInvalid, fetchCapInvalidMessage)
		return "", nil, false
	}
	claims, err := h.fetchCaps.CheckFetch(token, namespace, cid)
	switch {
	case err == nil:
		return namespace, claims, true
	case errors.Is(err, capability.ErrFetchInvalid):
		writeFetchCapError(w, http.StatusForbidden, CodeFetchCapInvalid, fetchCapInvalidMessage)
	case errors.Is(err, capability.ErrFetchRevoked):
		writeFetchCapError(w, http.StatusForbidden, CodeFetchCapRevoked, err.Error())
	default:
		h.logger.ComponentError(logging.ComponentGeneral, "a fetch capability could not be checked",
			zap.Error(err), zap.String("namespace", namespace))
		writeFetchCapError(w, http.StatusServiceUnavailable, CodeFetchCapUnavailable,
			"the fetch capability could not be checked right now")
	}
	return "", nil, false
}

// presentsCredential reports whether a request carries anything offered as a
// credential. A fetch capability names no account, and a credential beside it
// would.
func presentsCredential(r *http.Request) bool {
	ctx := r.Context()
	for _, key := range []ctxkeys.ContextKey{ctxkeys.JWT, ctxkeys.APIKey, ctxkeys.Scopes, ctxkeys.Grant, ctxkeys.Permissions} {
		if ctx.Value(key) != nil {
			return true
		}
	}
	return strings.TrimSpace(r.Header.Get("Authorization")) != "" ||
		gwauth.APIKeyFromRequest(r, true) != ""
}
