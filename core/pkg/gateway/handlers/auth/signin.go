package auth

import (
	"net/http"

	authsvc "github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/auth/siw"
)

// signIn is the whole of what a wallet signature buys, and both endpoints that
// accept one run it.
//
// Two separate things have to hold, and neither implies the other:
//
//   - the message is one this gateway would have issued — it parses, it names
//     this host, and its own deadline has not passed — and the wallet it names
//     signed exactly those bytes;
//   - the challenge inside it has not been spent. A signature is valid forever;
//     the nonce row is what makes it a single login.
//
// It writes the refusal itself and reports whether the caller may continue.
//
// Everything it returns comes out of the message rather than from the request
// body beside it, because the body is not signed: a caller who could name the
// namespace separately would be acting on a namespace the user never saw in the
// text they approved.
func (h *Handlers) signIn(w http.ResponseWriter, r *http.Request, message, signature string) (*signedIn, bool) {
	host, _, err := requestOrigin(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return nil, false
	}

	ctx := r.Context()
	m, err := h.authService.VerifySignedMessage(ctx, message, signature, host)
	if err != nil {
		h.recordSignInFailure(r, "", "", err)
		writeSignInError(w, err)
		return nil, false
	}

	namespace, err := authsvc.NamespaceOf(m)
	if err != nil {
		h.recordSignInFailure(r, "", m.Address, err)
		writeSignInError(w, err)
		return nil, false
	}

	if !h.consumeNonce(ctx, w, m.Address, m.Nonce, namespace) {
		h.recordSignInFailure(r, namespace, m.Address, authsvc.ErrNonceInvalid)
		return nil, false
	}

	return &signedIn{
		Message: m,
		// The message carries the EIP-55 checksummed address because the
		// grammar requires it; ownership rows, api_keys and the nonce table are
		// all keyed on the normalised form, so that is what everything after
		// this point uses. Two spellings of one wallet is how an owner stops
		// being an owner.
		Wallet:    authsvc.NormalizeWallet(m.Address),
		Namespace: namespace,
	}, true
}

// signedIn is a verified, single-use sign-in.
type signedIn struct {
	Message   *siw.Message
	Wallet    string
	Namespace string
}

func (h *Handlers) recordSignInFailure(r *http.Request, namespace, actor string, err error) {
	h.authService.Audit().RecordFromRequest(r.Context(), r, authsvc.AuditEvent{
		Namespace: namespace,
		Actor:     actor,
		Action:    authsvc.AuditVerifySucceeded,
		Result:    authsvc.AuditFailure,
		Metadata:  map[string]string{"reason": err.Error()},
	})
}
