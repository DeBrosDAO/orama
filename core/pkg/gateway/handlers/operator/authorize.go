package operator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// ErrCodeNotAnOperator is the machine-readable code for a credential that is
// valid but does not belong to an operator of this cluster.
const ErrCodeNotAnOperator = "NOT_AN_OPERATOR"

// requireOperator resolves the caller's wallet and refuses unless that wallet
// is on the cluster's operator list.
//
// /v1/operator/* had no scope entry and no ownership entry, so it fell through
// to "any valid credential is enough". `orama node invite` mints a cluster
// invite token, and an invite token holder is handed every secret the cluster
// has — including the cluster secret the JWT signing key is derived from. So
// any credential at all was a path to forging identities network-wide.
//
// Two things gate it now. The scope policy requires the admin grant, so a key
// out of an app bundle is refused before reaching this handler at all. And this
// checks the wallet against the operators table, so an admin key belonging to
// somebody who is not an operator is refused too.
//
// It reports and writes the response on refusal; a false return means the
// handler must return immediately.
// Authorize is requireOperator for a handler outside this package.
//
// An operator endpoint served from elsewhere — rotating the gateway's signing
// key — has to make the same check, and making it twice in two places is how
// the two drift apart.
func (h *Handler) Authorize(w http.ResponseWriter, r *http.Request) (string, bool) {
	return h.requireOperator(w, r)
}

func (h *Handler) requireOperator(w http.ResponseWriter, r *http.Request) (string, bool) {
	wallet := h.walletFromRequest(r)
	if wallet == "" {
		writeError(w, http.StatusUnauthorized, "wallet authentication required")
		return "", false
	}

	isOperator, err := h.isOperator(r.Context(), wallet)
	if err != nil {
		// Not knowing whether someone is an operator is not permission to
		// treat them as one.
		h.logger.Error("could not read the operator list", zap.Error(err))
		writeError(w, http.StatusServiceUnavailable,
			"cannot verify operator status right now; the registry did not answer")
		return "", false
	}
	if !isOperator {
		h.logger.Warn("refused a non-operator on an operator endpoint",
			zap.String("wallet", wallet), zap.String("path", r.URL.Path))
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error": "wallet " + wallet + " is not an operator of this cluster: " +
				"operating a node is what puts a wallet on that list",
			"code": ErrCodeNotAnOperator,
		})
		return "", false
	}
	return wallet, true
}

// isOperator reports whether a wallet is on the cluster's operator list.
func (h *Handler) isOperator(ctx context.Context, wallet string) (bool, error) {
	return IsOperator(ctx, h.rqliteClient, wallet)
}

// IsOperator reports whether a wallet is on the cluster's operator list.
//
// Exported because the gateway asks the same question of the raw-database
// routes: on the gateway that serves the cluster registry, exporting the
// database is an operator act, not a tenant one.
//
// The comparison is on the normalised address, because the same wallet is
// written checksummed in one place and lowercase in another and an operator
// locked out by capitalisation would be a worse bug than the one this closes.
func IsOperator(ctx context.Context, db rqlite.Client, wallet string) (bool, error) {
	if db == nil {
		return false, errNoRegistry
	}
	normalised := strings.ToLower(strings.TrimSpace(wallet))
	if normalised == "" {
		return false, nil
	}

	var rows []struct {
		Wallet string `db:"wallet"`
	}
	if err := db.Query(ctx, &rows,
		"SELECT wallet FROM operators WHERE LOWER(wallet) = ? LIMIT 1", normalised); err != nil {
		return false, err
	}
	return len(rows) > 0, nil
}

// errNoRegistry is returned when the handler has no database to ask.
var errNoRegistry = errString("operator handler has no registry client")

type errString string

func (e errString) Error() string { return string(e) }

// invite tokens
//
// The token column used to hold the raw token as its primary key, so anyone who
// could read the registry — a disk snapshot, a raw rqlite query, the export
// endpoint — could join the cluster and be handed every secret in it. What is
// stored is a hash now; what is handed to the operator is the only copy of the
// token that exists.
const inviteTokenHashPrefix = "sha256:"

// HashInviteToken is what goes in the database for a given invite token. The
// prefix is not decoration: migration 044 tells a converted row from a
// plaintext one by it, which is what makes that migration re-runnable.
func HashInviteToken(rawToken string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(rawToken)))
	return inviteTokenHashPrefix + hex.EncodeToString(sum[:])
}
