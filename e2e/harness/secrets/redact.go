// Package secrets keeps credentials out of everything the harness writes: CLI
// transcripts, HTTP evidence, collected journals and the report. It also holds
// the guard that stops a run from ever reaching the owner's real RootWallet.
package secrets

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Mask replaces every redacted value.
const Mask = "[REDACTED]"

// minValueLength is the shortest environment value redacted by value. Shorter
// values ("1", "e2e") would mangle unrelated output and are not credentials.
const minValueLength = 8

// MaxValues caps the literal values one redactor holds. Every Redact call
// scans for each of them, so the list must stay bounded; a run that mints
// more credentials than this is broken, and Add says so instead of silently
// forgetting one.
const MaxValues = 10000

// MaxTokenValues caps the JWTs, Orama API keys and refresh tokens one redactor
// holds; every sign-in mints a JWT and a refresh token. They are
// kept apart from the other values and replaced in one pass of a
// strings.Replacer, so they do not spend MaxValues: a run resumed across
// deploys mints thousands of them. Their shape patterns alone are not enough:
// two tokens printed back to back, or a token after a fragment that looks like
// a JWT's start, are matched across the boundary and leave the second token's
// payload and signature in clear text, where its literal masks it whole.
const MaxTokenValues = 100000

// MaxInputBytes bounds what one Redact call scans. Longer input is cut (on a
// rune boundary, with TruncatedMarker) before any pattern runs, so a runaway
// output cannot make redaction quadratic. Callers bound far below this.
const MaxInputBytes = 8 << 20

// TruncatedMarker ends input that Redact cut at MaxInputBytes.
const TruncatedMarker = "\n…[truncated before redaction]"

// ErrTooManyValues is returned by Add when MaxValues or MaxTokenValues would be
// exceeded.
var ErrTooManyValues = errors.New("secrets: too many literal values to redact")

// SecretEnvNames are the variables `infisical run` injects whose values are
// secret. Their values are redacted wherever they appear, whatever surrounds them.
var SecretEnvNames = []string{
	"HCLOUD_TOKEN",
	"CF_API_TOKEN",
	"INFISICAL_CLIENT_ID",
	"INFISICAL_CLIENT_SECRET",
	"INFISICAL_TOKEN",
	"BUGBOARD_MCP_TOKEN",
	"NTFY_URL",
	"RW_TEST_MNEMONIC",
}

// Redactor removes known secret values and recognised secret shapes from text.
// The zero value (and a nil *Redactor) redacts patterns only. It is safe for
// concurrent use: parallel tests register the tokens they mint while others
// redact.
type Redactor struct {
	mu     sync.RWMutex
	values []string
	// tokens are the values that are wholly a JWT or an Orama API key. They
	// are held apart from values only for their own cap: replacer replaces
	// both in one pass, longest first at each position, so a value that
	// contains another (a token, or a "key:secret" around one) is masked whole.
	// It is rebuilt by the first Redact after either changed.
	tokens   []string
	replacer *strings.Replacer
	stale    bool
	// seen holds every value accepted, so a repeat is not added twice. A value
	// refused at a cap is not in it: offered again, it is refused again.
	seen map[string]bool
	// sink persists newly added values (the run's token registry), so other
	// processes of the run redact them too.
	sink func([]string) error
}

// NewRedactor returns a redactor for the given literal values plus the built-in
// patterns. Values shorter than 8 characters are ignored.
func NewRedactor(values ...string) *Redactor {
	r := &Redactor{}
	literals, tokens := r.normalizeNew(values)
	r.accept(literals, tokens)
	return r
}

// FromEnv builds a redactor from the values of SecretEnvNames as lookup sees
// them (os.LookupEnv in production).
func FromEnv(lookup func(string) (string, bool)) *Redactor {
	var vals []string
	for _, name := range SecretEnvNames {
		if v, ok := lookup(name); ok {
			vals = append(vals, v)
		}
	}
	return NewRedactor(vals...)
}

// Add registers more literal values, for example a token minted during a test,
// and persists the new ones to the sink when one is set. It fails when the
// values cannot be persisted or would exceed MaxValues (MaxTokenValues for
// JWTs, Orama API keys and refresh tokens); the values it could hold are
// registered either way.
func (r *Redactor) Add(values ...string) error {
	r.mu.Lock()
	literals, tokens := r.normalizeNew(values)
	var capErr error
	if room := MaxValues - len(r.values); len(literals) > room {
		capErr = fmt.Errorf("%w: %d held, %d more offered, cap %d", ErrTooManyValues, len(r.values), len(literals), MaxValues)
		literals = literals[:max(room, 0)]
	}
	if room := MaxTokenValues - len(r.tokens); len(tokens) > room {
		capErr = errors.Join(capErr, fmt.Errorf("%w: %d tokens held, %d more offered, cap %d",
			ErrTooManyValues, len(r.tokens), len(tokens), MaxTokenValues))
		tokens = tokens[:max(room, 0)]
	}
	r.accept(literals, tokens)
	sink := r.sink
	r.mu.Unlock()
	fresh := make([]string, 0, len(literals)+len(tokens))
	fresh = append(append(fresh, literals...), tokens...)
	if sink != nil && len(fresh) > 0 {
		if err := sink(fresh); err != nil {
			return errors.Join(capErr, fmt.Errorf("failed to persist minted credentials for redaction: %w", err))
		}
	}
	return capErr
}

// normalizeNew returns the trimmed values long enough to redact that this
// redactor does not hold yet, without repeats, split into the tokens (wholly a
// JWT or an Orama API key) and the other literals. r.mu is held, or r is not
// shared yet.
func (r *Redactor) normalizeNew(values []string) (literals, tokens []string) {
	batch := make(map[string]bool, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if len(v) < minValueLength || r.seen[v] || batch[v] || strings.ContainsAny(v, "\r\n") {
			continue
		}
		batch[v] = true
		if wholeToken(v) {
			tokens = append(tokens, v)
		} else {
			literals = append(literals, v)
		}
	}
	return literals, tokens
}

// accept holds literals and tokens and marks them seen. r.mu is held, or r is
// not shared yet.
func (r *Redactor) accept(literals, tokens []string) {
	if len(literals)+len(tokens) == 0 {
		return
	}
	if r.seen == nil {
		r.seen = make(map[string]bool, len(literals)+len(tokens))
	}
	for _, v := range literals {
		r.seen[v] = true
	}
	for _, v := range tokens {
		r.seen[v] = true
	}
	r.values = append(r.values, literals...)
	r.tokens = append(r.tokens, tokens...)
	r.stale = true
}

func sortLongestFirst(vs []string) {
	sort.SliceStable(vs, func(i, j int) bool { return len(vs[i]) > len(vs[j]) })
}

// Redact returns s with every known value and every recognised secret masked.
func (r *Redactor) Redact(s string) string {
	s = boundTo(s, MaxInputBytes)
	if r != nil {
		if replacer := r.valueReplacer(); replacer != nil {
			s = replacer.Replace(s)
		}
	}
	return redactPatterns(s)
}

// valueReplacer is the replacer of every held value, rebuilt when they
// changed; nil when there are none. strings.Replacer tries its pairs in order
// at each position, so with the values longest first a value that contains
// another is masked whole.
func (r *Redactor) valueReplacer() *strings.Replacer {
	r.mu.RLock()
	replacer, stale := r.replacer, r.stale
	r.mu.RUnlock()
	if !stale {
		return replacer
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stale {
		all := make([]string, 0, len(r.values)+len(r.tokens))
		all = append(append(all, r.values...), r.tokens...)
		sortLongestFirst(all)
		pairs := make([]string, 0, 2*len(all))
		for _, v := range all {
			pairs = append(pairs, v, Mask)
		}
		r.replacer = nil
		if len(pairs) > 0 {
			r.replacer = strings.NewReplacer(pairs...)
		}
		r.stale = false
	}
	return r.replacer
}

// boundTo cuts s at limit bytes on a rune boundary.
func boundTo(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut] + TruncatedMarker
}
