package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/pkg/serverless"
	"github.com/DeBrosOfficial/network/pkg/serverless/registry"
	"go.uber.org/zap"
)

// Claims-provider hook (bugboard #548/#920).
//
// A namespace opts into additive, signed JWT claims by deploying a serverless
// function with the RESERVED name "auth-claims-provider". At /v1/auth/verify
// mint time the gateway invokes it (in the namespace's own context, so it can
// read the namespace's tables) with {"wallet","namespace"} and merges the
// string→string object it returns into the JWT's custom claims — e.g.
// {"account_id":"<users.user_id>"} so push devices key on the stable account
// identity rather than the authenticating wallet.
//
// Hard guarantees:
//   - FAIL-OPEN: a missing / slow / erroring / malformed provider yields NO
//     claims; authentication never breaks because a claims function is down.
//   - Reserved claims (sub/iss/aud/iat/nbf/exp/namespace/custom) can never be
//     set by the provider — the gateway controls those.
//   - Bounded: timeout, max claim count, max total size.

const (
	// claimsProviderFnName is the reserved function name a namespace deploys to
	// inject additive JWT claims at mint time.
	claimsProviderFnName = "auth-claims-provider"
	// claimsProviderTimeout bounds the provider invocation so a slow/hung
	// function never stalls the auth path past this budget (fail-open after).
	claimsProviderTimeout = 2 * time.Second
	// maxCustomClaims / maxCustomClaimsBytes cap what a provider may inject —
	// JWTs ride in headers, and an unbounded claim blob is a DoS / cost vector.
	maxCustomClaims      = 16
	maxCustomClaimsBytes = 4096
	// claimsProviderWarnInterval rate-limits the fail-open WARN so a broken
	// provider doesn't flood the log on every login.
	claimsProviderWarnInterval = 30 * time.Second
	// claimsProviderMaxAttempts bounds how many times ResolveClaims re-invokes
	// the provider on a TRANSIENT infra failure (cold WASM fetch, transient
	// invoke error). The provider is a serverless function subject to cold-WASM
	// stalls (bugboard #143): a single attempt fails open under a cold start,
	// dropping account_id and fragmenting the user's devices. Each attempt is
	// independently bounded by claimsProviderTimeout, so the total budget stays
	// bounded; fail-open still holds after the last attempt.
	claimsProviderMaxAttempts = 3
	// claimsProviderRetryBackoff is the short pause between transient-failure
	// retries — enough to let an in-flight cold WASM fetch land, without adding
	// meaningful latency to the login path.
	claimsProviderRetryBackoff = 150 * time.Millisecond
)

// reservedClaimKeys can never be injected by a namespace claims provider; the
// gateway owns these. A provider that returns any of them has them dropped.
var reservedClaimKeys = map[string]struct{}{
	"sub": {}, "iss": {}, "aud": {}, "iat": {},
	"nbf": {}, "exp": {}, "namespace": {}, "custom": {},
	// "scopes" is the gateway-authoritative API-key grant set (bugboard #148).
	// A namespace claims provider must NEVER be able to inject it — otherwise a
	// tenant could mint admin for every end-user's JWT. Only the API-key→JWT
	// exchange path sets it, and callerScopes only trusts it on an ak_ subject.
	"scopes": {},
}

// claimsInvoker is the narrow invoke seam the claims provider depends on —
// satisfied by *serverless.Invoker in production and by a fake in tests so the
// retry behaviour (bugboard #143) can be exercised without a live WASM engine.
type claimsInvoker interface {
	Invoke(ctx context.Context, req *serverless.InvokeRequest) (*serverless.InvokeResponse, error)
}

// jwtClaimsProvider implements auth.ClaimsResolver by invoking the namespace's
// reserved auth-claims-provider function.
type jwtClaimsProvider struct {
	invoker claimsInvoker
	logger  *zap.Logger

	mu          sync.Mutex
	lastWarnUTC time.Time
}

// newJWTClaimsProvider builds the resolver. A nil invoker disables the hook
// (ResolveClaims returns nil).
func newJWTClaimsProvider(invoker *serverless.Invoker, logger *zap.Logger) *jwtClaimsProvider {
	if logger == nil {
		logger = zap.NewNop()
	}
	p := &jwtClaimsProvider{logger: logger.Named("claims-provider")}
	// Guard against a typed-nil interface: a nil *serverless.Invoker stored in
	// the claimsInvoker interface would be != nil, defeating the disable check
	// in ResolveClaims. Only assign when the concrete pointer is non-nil.
	if invoker != nil {
		p.invoker = invoker
	}
	return p
}

// ResolveClaims invokes the namespace's auth-claims-provider and returns the
// sanitized additive claims, or nil. Never errors (fail-open contract).
func (p *jwtClaimsProvider) ResolveClaims(ctx context.Context, wallet, namespace string) map[string]string {
	if p.invoker == nil || wallet == "" || namespace == "" {
		return nil
	}

	input, err := json.Marshal(map[string]string{"wallet": wallet, "namespace": namespace})
	if err != nil {
		return nil
	}

	var lastErr error
	for attempt := 0; attempt < claimsProviderMaxAttempts; attempt++ {
		if ctx.Err() != nil { // parent cancelled/expired — stop early, fail open
			break
		}
		resp, err := p.invokeOnce(ctx, namespace, input)
		if err == nil && resp != nil && resp.Status == serverless.InvocationStatusSuccess {
			return sanitizeProviderClaims(resp.Output)
		}
		if errors.Is(err, registry.ErrFunctionNotFound) {
			// No provider deployed — the normal no-claims case. Stay silent, no retry.
			return nil
		}
		if !p.retryable(err) {
			// A clean non-success result is the app's own logic, and a non-
			// transient invoke error is not going to recover on retry — fail
			// open once. A non-success result is not an err; note it below.
			if err != nil {
				p.warnRateLimited("claims provider invoke failed (minting without custom claims)",
					namespace, err)
			} else {
				p.warnRateLimited("claims provider returned non-success (minting without custom claims)",
					namespace, nil)
			}
			return nil
		}
		lastErr = err
		if attempt < claimsProviderMaxAttempts-1 {
			time.Sleep(claimsProviderRetryBackoff)
		}
	}

	// Exhausted retries on a transient failure (or parent ctx done) — fail open.
	p.warnRateLimited("claims provider transient failure exhausted retries (minting without custom claims)",
		namespace, lastErr)
	return nil
}

// invokeOnce performs a single provider invocation bounded by
// claimsProviderTimeout derived from the parent ctx.
func (p *jwtClaimsProvider) invokeOnce(ctx context.Context, namespace string, input []byte) (*serverless.InvokeResponse, error) {
	callCtx, cancel := context.WithTimeout(ctx, claimsProviderTimeout)
	defer cancel()
	return p.invoker.Invoke(callCtx, &serverless.InvokeRequest{
		Namespace:    namespace,
		FunctionName: claimsProviderFnName,
		Input:        input,
		// Gateway-initiated, no end-user caller → system trigger skips the
		// per-caller authorization check.
		TriggerType: serverless.TriggerTypeInternal,
	})
}

// retryable reports whether an invoke error is a TRANSIENT infra failure worth
// re-attempting. Cold WASM fetch (ErrWASMFetchTimeout) is the concrete signal
// (bugboard #143): the block is not-yet-replicated on this node and lands on a
// subsequent attempt. Everything else — a clean non-success result (nil err),
// ErrFunctionNotFound, or a genuine invoke error — is not retried.
func (p *jwtClaimsProvider) retryable(err error) bool {
	return errors.Is(err, serverless.ErrWASMFetchTimeout)
}

// sanitizeProviderClaims parses the provider's RAW stdout as a bare JSON object
// of additive claims (NOT an {ok,result} Ack envelope — per the #976 contract)
// and returns a safe string→string subset: string values only, reserved keys
// dropped, bounded count and total size. Any parse failure → nil (fail-open).
func sanitizeProviderClaims(raw []byte) map[string]string {
	if len(raw) == 0 || len(raw) > maxCustomClaimsBytes {
		return nil
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil || len(obj) == 0 {
		return nil
	}
	// Iterate in sorted key order so an over-budget provider payload truncates
	// DETERMINISTICALLY (Go map iteration is randomized) — the same output must
	// always yield the same claims, never a per-login-varying subset.
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make(map[string]string, len(obj))
	total := 0
	for _, k := range keys {
		if len(out) >= maxCustomClaims {
			break
		}
		if _, reserved := reservedClaimKeys[k]; reserved {
			continue
		}
		s, ok := obj[k].(string) // string→string contract; non-string values dropped
		if !ok {
			continue
		}
		total += len(k) + len(s)
		if total > maxCustomClaimsBytes {
			break
		}
		out[k] = s
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (p *jwtClaimsProvider) warnRateLimited(msg, namespace string, err error) {
	p.mu.Lock()
	now := time.Now()
	if now.Sub(p.lastWarnUTC) < claimsProviderWarnInterval {
		p.mu.Unlock()
		return
	}
	p.lastWarnUTC = now
	p.mu.Unlock()

	fields := []zap.Field{zap.String("namespace", namespace), zap.String("function", claimsProviderFnName)}
	if err != nil {
		fields = append(fields, zap.Error(err))
	}
	p.logger.Warn(msg, fields...)
}
