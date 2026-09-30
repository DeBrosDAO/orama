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

// MaxInputBytes bounds what one Redact call scans. Longer input is cut (on a
// rune boundary, with TruncatedMarker) before any pattern runs, so a runaway
// output cannot make redaction quadratic. Callers bound far below this.
const MaxInputBytes = 8 << 20

// TruncatedMarker ends input that Redact cut at MaxInputBytes.
const TruncatedMarker = "\n…[truncated before redaction]"

// ErrTooManyValues is returned by Add when MaxValues would be exceeded.
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
	// sink persists newly added values (the run's token registry), so other
	// processes of the run redact them too.
	sink func([]string) error
}

// NewRedactor returns a redactor for the given literal values plus the built-in
// patterns. Values shorter than 8 characters are ignored.
func NewRedactor(values ...string) *Redactor {
	r := &Redactor{}
	r.values = normalizeNew(nil, values)
	sortLongestFirst(r.values)
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
// values cannot be persisted or would exceed MaxValues; the values it could
// hold are registered either way.
func (r *Redactor) Add(values ...string) error {
	r.mu.Lock()
	fresh := normalizeNew(r.values, values)
	var capErr error
	if room := MaxValues - len(r.values); len(fresh) > room {
		capErr = fmt.Errorf("%w: %d held, %d more offered, cap %d", ErrTooManyValues, len(r.values), len(fresh), MaxValues)
		fresh = fresh[:max(room, 0)]
	}
	r.values = append(r.values, fresh...)
	// Longest first, so a value that contains another is masked whole.
	sortLongestFirst(r.values)
	sink := r.sink
	r.mu.Unlock()
	if sink != nil && len(fresh) > 0 {
		if err := sink(fresh); err != nil {
			return errors.Join(capErr, fmt.Errorf("failed to persist minted credentials for redaction: %w", err))
		}
	}
	return capErr
}

// normalizeNew returns the trimmed values long enough to redact that are not
// in have and not repeated.
func normalizeNew(have, values []string) []string {
	seen := make(map[string]bool, len(have))
	for _, v := range have {
		seen[v] = true
	}
	var out []string
	for _, v := range values {
		v = strings.TrimSpace(v)
		if len(v) < minValueLength || seen[v] || strings.ContainsAny(v, "\r\n") {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func sortLongestFirst(vs []string) {
	sort.SliceStable(vs, func(i, j int) bool { return len(vs[i]) > len(vs[j]) })
}

// Redact returns s with every known value and every recognised secret masked.
func (r *Redactor) Redact(s string) string {
	s = boundTo(s, MaxInputBytes)
	if r != nil {
		r.mu.RLock()
		for _, v := range r.values {
			s = strings.ReplaceAll(s, v, Mask)
		}
		r.mu.RUnlock()
	}
	return redactPatterns(s)
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
