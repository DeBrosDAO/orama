package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
)

// Nothing recorded who minted a key, who was granted what, who revoked it, or
// who signed in. The table to hold it has existed since the first migration and
// was never written to, so the first question anyone asks about a credential —
// when did this appear, and who made it — had no answer anywhere.
//
// These are the events worth a durable record. They are all rare: a login, a
// key being minted or revoked, an operator acting. A refused request is not on
// the list, deliberately — writing one per 401 would let anyone with a network
// connection fill a Raft-replicated table.

const (
	AuditChallengeIssued   = "auth.challenge"
	AuditVerifySucceeded   = "auth.verify"
	AuditRefreshed         = "auth.refresh"
	AuditRefreshReplayed   = "auth.refresh.replay"
	AuditLoggedOut         = "auth.logout"
	AuditKeyIssued         = "key.issue"
	AuditKeyRevoked        = "key.revoke"
	AuditKeyRotated        = "key.rotate"
	AuditKeysRevokedBulk   = "key.revoke_all"
	AuditNamespaceCreated  = "namespace.create"
	AuditNamespaceDeleted  = "namespace.delete"
	AuditSecretSet         = "secret.set"
	AuditSecretDeleted     = "secret.delete"
	AuditFunctionDeployed  = "function.deploy"
	AuditFunctionDeleted   = "function.delete"
	AuditDeploymentCreated = "deployment.deploy"
	AuditDeploymentDeleted = "deployment.delete"
	AuditOperatorAction    = "operator.action"
	// Who was given authority in a namespace, and who took it away. A grant is
	// the thing an incident asks about after the fact.
	AuditGrantAdded       = "grant.add"
	AuditGrantRevoked     = "grant.revoke"
	AuditOwnerTransferred = "namespace.transfer"
)

// AuditActions is every action this gateway records. A new one has to be added
// here, which is what keeps `orama audit` and the docs able to describe what
// they are showing.
var AuditActions = []string{
	AuditChallengeIssued, AuditVerifySucceeded, AuditRefreshed, AuditRefreshReplayed,
	AuditLoggedOut, AuditKeyIssued, AuditKeyRevoked, AuditKeyRotated, AuditKeysRevokedBulk,
	AuditNamespaceCreated, AuditNamespaceDeleted, AuditSecretSet, AuditSecretDeleted,
	AuditFunctionDeployed, AuditFunctionDeleted, AuditDeploymentCreated, AuditDeploymentDeleted,
	AuditOperatorAction,
	AuditGrantAdded, AuditGrantRevoked, AuditOwnerTransferred,
}

const (
	AuditSuccess = "success"
	AuditFailure = "failure"
)

// AuditEvent is one line of the record.
type AuditEvent struct {
	// Namespace the event belongs to. Empty for a cluster-level event.
	Namespace string
	// Actor is who did it: a wallet, an API key's id, or "system".
	Actor string
	// Action is one of the constants above.
	Action string
	// Resource is what it was done to, when that is not the actor.
	Resource string
	// Result is AuditSuccess or AuditFailure.
	Result string
	IP     string
	// UserAgent as sent. Truncated; it is attacker-controlled text.
	UserAgent string
	// Metadata is anything else worth keeping. Never a credential.
	Metadata map[string]string
}

// maxAuditFieldLength bounds the fields a caller controls. A user agent is
// whatever the client sends, and this table is replicated to every node.
const maxAuditFieldLength = 512

// AuditLog writes the record.
type AuditLog struct {
	orm    client.NetworkClient
	logger *logging.ColoredLogger
}

// NewAuditLog builds the writer. A nil orm makes every write a no-op, which is
// the test case; a gateway always has one.
func NewAuditLog(orm client.NetworkClient, logger *logging.ColoredLogger) *AuditLog {
	return &AuditLog{orm: orm, logger: logger}
}

// Record writes one event.
//
// A failed write is logged and not returned. The record is evidence, not a
// control: refusing a login because the audit row could not be written would
// turn a database blip into an outage, and the caller has already been
// authenticated or refused on its own merits by the time this runs.
func (a *AuditLog) Record(ctx context.Context, event AuditEvent) {
	if a == nil || a.orm == nil {
		return
	}
	db := a.orm.Database()
	if db == nil {
		return
	}

	if strings.TrimSpace(event.Action) == "" {
		return
	}
	if event.Result == "" {
		event.Result = AuditSuccess
	}

	metadata := ""
	if len(event.Metadata) > 0 {
		if encoded, err := json.Marshal(event.Metadata); err == nil {
			metadata = truncate(string(encoded), maxAuditFieldLength*4)
		}
	}

	internalCtx := client.WithInternalAuth(ctx)
	if _, err := db.Query(internalCtx,
		`INSERT INTO audit_events(namespace, actor, action, resource, result, ip, user_agent, metadata)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		nullable(strings.TrimSpace(event.Namespace)),
		nullable(truncate(event.Actor, maxAuditFieldLength)),
		event.Action,
		nullable(truncate(event.Resource, maxAuditFieldLength)),
		event.Result,
		nullable(truncate(event.IP, maxAuditFieldLength)),
		nullable(truncate(event.UserAgent, maxAuditFieldLength)),
		nullable(metadata),
	); err != nil && a.logger != nil {
		a.logger.ComponentWarn(logging.ComponentGeneral,
			"an auth event was not recorded; the audit trail has a hole in it",
			zap.String("action", event.Action),
			zap.String("namespace", event.Namespace),
			zap.Error(err))
	}
}

// RecordFromRequest fills in the parts of an event that come from the request.
func (a *AuditLog) RecordFromRequest(ctx context.Context, r *http.Request, event AuditEvent) {
	if r != nil {
		if event.IP == "" {
			event.IP = clientIP(r)
		}
		if event.UserAgent == "" {
			event.UserAgent = r.Header.Get("User-Agent")
		}
	}
	a.Record(ctx, event)
}

// clientIP is the address to record. X-Forwarded-For is what the reverse proxy
// in front of the gateway sets; its first entry is the client.
func clientIP(r *http.Request) string {
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwarded != "" {
		if comma := strings.IndexByte(forwarded, ','); comma >= 0 {
			return strings.TrimSpace(forwarded[:comma])
		}
		return forwarded
	}
	return r.RemoteAddr
}

func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max]
}

// Audit exposes the log so handlers outside this package can record.
//
// Nil-safe on both ends: a gateway built without an auth service (tests, and
// the index gateway before it has one) hands the resulting nil log to its
// handlers, and every method on it is a no-op.
func (s *Service) Audit() *AuditLog {
	if s == nil {
		return nil
	}
	return s.audit
}

const (
	// AuditRetention is how long an event is kept.
	//
	// The table is Raft-replicated to every node and every authenticated
	// request can add to it, so it grows without bound unless something
	// removes rows — which is the shape of bug-237. Ninety days is long enough
	// to answer "what happened last quarter" and short enough that the table
	// stays a log rather than an archive.
	AuditRetention = 90 * 24 * time.Hour

	// auditPruneInterval is how often old events are removed. The table is not
	// hot, so this is deliberately slow.
	auditPruneInterval = 6 * time.Hour

	// auditPruneBatch bounds one delete. A cluster that has not pruned in a
	// long time has a lot of rows, and one enormous DELETE is a Raft entry
	// every node has to apply at once.
	auditPruneBatch = 5000
)

// Prune removes events past the retention window. It does not loop: the next
// tick takes the next batch.
//
// It returns no count. The database client answers a write with a fixed
// "success" row rather than the number of rows affected, so any number here
// would be made up — and a made-up count is worse than none, because it reads
// like a measurement.
func (a *AuditLog) Prune(ctx context.Context) error {
	if a == nil || a.orm == nil {
		return nil
	}
	db := a.orm.Database()
	if db == nil {
		return nil
	}

	_, err := db.Query(client.WithInternalAuth(ctx),
		`DELETE FROM audit_events
		  WHERE id IN (
		      SELECT id FROM audit_events
		       WHERE created_at < datetime('now', ?)
		       ORDER BY id LIMIT ?)`,
		fmt.Sprintf("-%d seconds", int64(AuditRetention.Seconds())), auditPruneBatch)
	if err != nil {
		return fmt.Errorf("prune the audit trail: %w", err)
	}
	return nil
}

// StartPruning removes expired events on a timer until ctx is done.
func (a *AuditLog) StartPruning(ctx context.Context) {
	if a == nil || a.orm == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(auditPruneInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := a.Prune(ctx); err != nil && a.logger != nil {
					a.logger.ComponentWarn(logging.ComponentGeneral,
						"could not prune the audit trail; it will keep growing", zap.Error(err))
				}
			}
		}
	}()
}

// ActorFromRequest is who to record for a request: the JWT subject, redacted
// if it is a credential rather than a wallet. Returns "" when the request was
// not JWT-authenticated, which records as no actor rather than a wrong one.
func ActorFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	claims, ok := r.Context().Value(ctxkeys.JWT).(*JWTClaims)
	if !ok || claims == nil {
		return ""
	}
	return RedactSubject(claims.Sub)
}

// RedactSubject returns a subject that is safe to store.
//
// The JWT minted by the API-key exchange carries the key ITSELF as its subject
// (pkg/gateway/handlers/auth/jwt_handler.go), so recording a subject verbatim
// would put a live credential in a Raft-replicated table that every owner can
// read back over GET /v1/audit. A wallet is an identity and is kept; anything
// else is recorded as a fingerprint, which is stable enough to group one
// caller's events together and reveals nothing.
func RedactSubject(sub string) string {
	sub = strings.TrimSpace(sub)
	if sub == "" || IsWalletSubject(sub) {
		return sub
	}
	sum := sha256.Sum256([]byte(sub))
	return "key:" + hex.EncodeToString(sum[:])[:16]
}

// IsAuditAction reports whether an action is one this gateway records. It is
// what lets a filter refuse a typo instead of returning an empty page.
func IsAuditAction(action string) bool {
	for _, known := range AuditActions {
		if known == action {
			return true
		}
	}
	return false
}
