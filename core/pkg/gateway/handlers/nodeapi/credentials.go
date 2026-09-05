package nodeapi

import (
	"context"
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/auth"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// Credentials is what the cluster knows about which node is which.
//
// The rule it applies has two cases and no fallback between them: a node with a
// live key on record is verified against that key and nothing else, and a node
// with no live key is verified against nothing at all. Nothing derived from the
// cluster secret is accepted here, so a machine holding every shared secret the
// cluster has still cannot speak as a node it is not.
//
// A node with no row has not enrolled yet, and its way in is the enrolment
// endpoint — which is authenticated against the key carried inside its own peer
// id, not against anything shared. So there is no trust-on-first-use window to
// close: this is what Phase B of the node-identity work was going to be, and
// the identity proof made it available now instead.
//
// A revoked node lands in the second case, which is the point: a retired
// machine's disk stops working the moment it is retired, rather than when the
// cluster secret is next rotated — which invalidates every token in the cluster
// at once, so in practice it never is.
type Credentials struct {
	db rqlite.Client
}

// NewCredentials builds the store.
func NewCredentials(db rqlite.Client) *Credentials {
	return &Credentials{db: db}
}

// credentialRow is one node's recorded key.
type credentialRow struct {
	PublicKey string `db:"public_key"`
	RevokedAt string `db:"revoked_at"`
}

// VerifierFor answers how to check a stamp claiming to be from a node.
func (c *Credentials) VerifierFor(ctx context.Context) auth.NodeVerifierFor {
	return func(nodeID string) (auth.NodeStampVerifier, error) {
		row, found, err := c.lookup(ctx, nodeID)
		if err != nil {
			return nil, err
		}
		if !found || strings.TrimSpace(row.RevokedAt) != "" {
			// Never enrolled, or retired. Nothing verifies for it. This is
			// deliberately not "fall back to something shared": that is what
			// would let a compromised node speak for this one, and for a
			// retired node it would make revocation mean nothing.
			return nil, nil
		}
		return auth.ParseNodePublicKey(row.PublicKey)
	}
}

// lookup reads a node's recorded key.
func (c *Credentials) lookup(ctx context.Context, nodeID string) (credentialRow, bool, error) {
	var rows []credentialRow
	if err := c.db.Query(ctx, &rows,
		`SELECT public_key, COALESCE(revoked_at, '') AS revoked_at FROM node_credentials WHERE node_id = ?`,
		nodeID); err != nil {
		return credentialRow{}, false, fmt.Errorf("read the recorded key for node %s: %w", nodeID, err)
	}
	if len(rows) == 0 {
		return credentialRow{}, false, nil
	}
	return rows[0], true, nil
}

// enrolOutcome is what an enrolment did, for the audit record and the answer.
type enrolOutcome string

const (
	enrolRecorded  enrolOutcome = "recorded"
	enrolUnchanged enrolOutcome = "unchanged"
)

// Enrol records a node's key the first time it presents one.
//
// It refuses two things, and the refusals are the point:
//
//   - a different key for a node that already has a live one. The cluster
//     cannot tell a rotation from a takeover, so it refuses both. Re-keying a
//     rebuilt machine happens at the join, which clears the row under an
//     operator-minted invite.
//   - any key for a revoked node. A retired machine must not be able to
//     re-admit itself; that is an operator's decision.
//
// Presenting the same key again is not a change, and says so — a node re-asserts
// its key on every start, and that must not read as an attempted takeover.
func (c *Credentials) Enrol(ctx context.Context, nodeID, publicKey string) (enrolOutcome, error) {
	if _, err := auth.ParseNodePublicKey(publicKey); err != nil {
		return "", err
	}

	row, found, err := c.lookup(ctx, nodeID)
	if err != nil {
		return "", err
	}
	if found {
		if strings.TrimSpace(row.RevokedAt) != "" {
			return "", fmt.Errorf("this node was retired; re-admitting it is an operator action")
		}
		if row.PublicKey != publicKey {
			return "", fmt.Errorf("this node already has a different key on record")
		}
		return enrolUnchanged, nil
	}

	// Only when no row exists. An upsert here would be the takeover the check
	// above refuses, written in SQL — and `INSERT ... WHERE NOT EXISTS` keeps
	// that true against a second caller racing this one, since the table is
	// replicated and the read above is not a lock.
	res, err := c.db.Exec(ctx,
		`INSERT INTO node_credentials (node_id, public_key)
		 SELECT ?, ? WHERE NOT EXISTS (SELECT 1 FROM node_credentials WHERE node_id = ?)`,
		nodeID, publicKey, nodeID)
	if err != nil {
		return "", fmt.Errorf("record the key for node %s: %w", nodeID, err)
	}
	if res != nil {
		if affected, aerr := res.RowsAffected(); aerr == nil && affected == 0 {
			// Somebody else inserted between the read and the write. Whether
			// that was this node retrying or something else, this call did not
			// establish the key and must not report that it did.
			return "", fmt.Errorf("this node already has a key on record")
		}
	}
	return enrolRecorded, nil
}
