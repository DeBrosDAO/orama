package storage

import (
	"context"
	"net/http"

	gwauth "github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/httputil"
)

// A grant may be narrowed to a path prefix — `storage:avatars/*` — and this is
// where that is applied.
//
// Before it, a `storage` grant was every object in the namespace, so one leaked
// runtime key read everything the application had ever uploaded. A credential
// with no selector is unaffected: the scope gate has already decided whether it
// may reach storage at all, and this only ever takes access away.
//
// The name a selector matches is the one the uploader gave, normalised. It is
// client-supplied, and that is the model — the selector says where you may
// write, and you name what you write, the way an object store does. What it
// must not be is ambiguous, which is what NormalizeStoragePath is for.

// authorizeStoragePath refuses a request whose grant does not cover the object.
func (h *Handlers) authorizeStoragePath(w http.ResponseWriter, r *http.Request, path string, action gwauth.Action) bool {
	err := gwauth.AuthorizeResource(r.Context(), gwauth.Resource{
		Domain: gwauth.SelectorStorage,
		Name:   path,
		Action: action,
	})
	if err == nil {
		return true
	}
	httputil.WriteError(w, http.StatusForbidden, err.Error())
	return false
}

// authorizeCID refuses a request for a CID whose recorded name the caller's
// grant does not cover.
//
// A CID this namespace has no row for, or one whose row has no name, is a
// resource this code cannot name. For a grant with a selector that is a
// refusal: "I could not work out what you are touching" is not a reason to
// allow it, and every object uploaded from here on has a name. A grant with no
// selector is not narrowed and reaches it as before.
func (h *Handlers) authorizeCID(w http.ResponseWriter, r *http.Request, cid, namespace string, action gwauth.Action) bool {
	path, err := h.pathOfCID(r.Context(), cid, namespace)
	if err != nil {
		httputil.WriteError(w, http.StatusServiceUnavailable,
			"could not read what this object is called, so a narrowed grant cannot be checked against it")
		return false
	}
	return h.authorizeStoragePath(w, r, path, action)
}

// pathOfCID returns the normalised name a namespace recorded for a CID, or ""
// when it has none.
func (h *Handlers) pathOfCID(ctx context.Context, cid, namespace string) (string, error) {
	if h.db == nil {
		return "", nil
	}
	var rows []map[string]interface{}
	if err := h.db.Query(ctx, &rows,
		`SELECT name FROM ipfs_content_ownership WHERE cid = ? AND namespace = ? LIMIT 1`,
		cid, namespace); err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", nil
	}
	name, _ := rows[0]["name"].(string)
	// Normalised on the way out as well as in: rows written before names were
	// normalised are still in the table, and a selector has to compare against
	// one canonical string.
	normalized, err := gwauth.NormalizeStoragePath(name)
	if err != nil {
		// A stored name that cannot be normalised is one nothing can match, so
		// it is reported as unnamed rather than as an error.
		return "", nil
	}
	return normalized, nil
}
