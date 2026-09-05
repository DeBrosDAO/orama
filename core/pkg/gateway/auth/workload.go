package auth

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
)

// A deployment's own identity.
//
// A deployed app used to receive `PORT`, its namespace and its gateway's URL,
// and no credential at all — so every app that talked to the platform it runs
// on carried a key somebody had pasted into it. That key is a namespace key: an
// application compromise was a namespace takeover, and nothing a workload did
// was attributable to the workload.
//
// A deployment is a principal now, with grants its owner chooses, and it is
// handed a token rather than a key. The token is short-lived and renewable, so
// the thing on the node expires by itself and a revoked deployment stops being
// able to renew.

// PrincipalApp is a deployed application.
const PrincipalApp PrincipalType = "app"

// WorkloadTokenLifetime is how long a workload token is good for.
//
// Short enough that a token read off a node is worth little, long enough that a
// deployment which is slow to start, or briefly unable to reach its gateway,
// does not lose its identity before it has used it. It renews at half of this;
// see RenewWorkloadToken.
const WorkloadTokenLifetime = time.Hour

// WorkloadSubjectPrefix is what tells a workload subject from a wallet or a
// key. It is not a credential, so nothing is leaked by it appearing in a log.
const WorkloadSubjectPrefix = "app:"

// WorkloadSubject is the subject of a deployment's token, and the identifier of
// its principal.
func WorkloadSubject(namespace, name string) string {
	return WorkloadSubjectPrefix + strings.TrimSpace(namespace) + "/" + strings.TrimSpace(name)
}

// IsWorkloadSubject reports whether a token subject belongs to a workload.
func IsWorkloadSubject(subject string) bool {
	return strings.HasPrefix(strings.TrimSpace(subject), WorkloadSubjectPrefix)
}

// EnsureWorkloadPrincipal records a deployment as a principal, so grants can be
// written against it before it has ever run.
//
// It creates no grant. A deployment that nobody has given anything to reaches
// nothing, which is the only safe default: the alternative is every deployment
// starting with the namespace's data plane, which is the permanent key this
// replaces wearing a different hat.
func (s *Service) EnsureWorkloadPrincipal(ctx context.Context, namespace, name string) error {
	orm := s.keyORM()
	if orm == nil {
		return fmt.Errorf("no registry is configured, so a deployment cannot be given an identity")
	}
	db := orm.Database()
	_, err := s.ensurePrincipal(ctx, db, PrincipalApp,
		WorkloadSubject(namespace, name), namespace+"/"+name, "deploy")
	return err
}

// MintWorkloadToken issues a deployment's token.
//
// The grants are whatever its principal holds in its own namespace, resolved at
// mint time rather than baked in — so revoking a grant takes effect on the next
// renewal rather than only on the next deploy.
func (s *Service) MintWorkloadToken(ctx context.Context, namespace, name string) (string, time.Time, error) {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" || strings.TrimSpace(name) == "" {
		return "", time.Time{}, fmt.Errorf("a workload token needs a namespace and a deployment name")
	}

	subject := WorkloadSubject(namespace, name)
	scopes, err := s.workloadScopes(ctx, namespace, subject)
	if err != nil {
		return "", time.Time{}, err
	}

	token, expUnix, err := s.GenerateJWT(namespace, subject, WorkloadTokenLifetime,
		map[string]string{"scopes": scopes.Canonical()})
	if err != nil {
		return "", time.Time{}, fmt.Errorf("mint the workload token for %s: %w", subject, err)
	}
	return token, time.Unix(expUnix, 0), nil
}

// workloadScopes is what a deployment's principal may do in its namespace.
//
// A principal with no grant gets an empty set, not the data plane. A missing
// grant is not a reason to hand out more than was asked for.
func (s *Service) workloadScopes(ctx context.Context, namespace, subject string) (ScopeSet, error) {
	orm := s.keyORM()
	if orm == nil {
		return ScopeSet{}, fmt.Errorf("no registry is configured, so a workload's grants cannot be read")
	}
	db := orm.Database()

	internalCtx := client.WithInternalAuth(ctx)
	res, err := db.Query(internalCtx, "SELECT id FROM namespaces WHERE name = ? LIMIT 1", namespace)
	if err != nil {
		return ScopeSet{}, fmt.Errorf("resolve the namespace %s: %w", namespace, err)
	}
	if res == nil || res.Count == 0 || len(res.Rows) == 0 || len(res.Rows[0]) == 0 {
		return ScopeSet{}, fmt.Errorf("no such namespace: %s", namespace)
	}
	nsID := res.Rows[0][0]
	grant, err := s.GrantIn(ctx, db, nsID, PrincipalApp, subject)
	if err != nil {
		// No grant is not an error the caller has to distinguish: the token is
		// still issued, and it reaches nothing until somebody grants it
		// something. An app with no grants that could not start at all would
		// make deploying one a two-step operation for no benefit.
		return ScopeSet{}, nil
	}
	return grant.Scopes(), nil
}

// RenewWorkloadToken issues a fresh token to the holder of a live one.
//
// This is what keeps the credential on the node short-lived without needing
// anything privileged to rewrite it: the deployment reads its first token from
// a file systemd staged for it, and asks for the next one with the one it has.
// A deployment whose principal has been revoked cannot renew, and the token it
// is holding expires on its own.
func (s *Service) RenewWorkloadToken(ctx context.Context, claims *JWTClaims) (string, time.Time, error) {
	if claims == nil || !IsWorkloadSubject(claims.Sub) {
		return "", time.Time{}, fmt.Errorf("only a workload's own token can be renewed")
	}
	namespace, name, ok := ParseWorkloadSubject(claims.Sub)
	if !ok {
		return "", time.Time{}, fmt.Errorf("the token's subject %q does not name a deployment", claims.Sub)
	}
	if !strings.EqualFold(namespace, claims.Namespace) {
		return "", time.Time{}, fmt.Errorf("the token's subject names %q and its claim names %q", namespace, claims.Namespace)
	}
	return s.MintWorkloadToken(ctx, namespace, name)
}

// ParseWorkloadSubject splits a workload subject back into its namespace and
// deployment name.
func ParseWorkloadSubject(subject string) (namespace, name string, ok bool) {
	rest, found := strings.CutPrefix(strings.TrimSpace(subject), WorkloadSubjectPrefix)
	if !found {
		return "", "", false
	}
	namespace, name, found = strings.Cut(rest, "/")
	if !found || namespace == "" || name == "" {
		return "", "", false
	}
	return namespace, name, true
}
