package host

import (
	"strings"
)

// Fake is an in-memory Host for tests and for running a function off the
// gateway. The cache and the caller are real; the database answers through the
// hooks, because what a statement does is the test's to say.
type Fake struct {
	Wallet   string
	Counters map[string]int64
	Logs     []string
	// OnQuery and OnExec answer DBQuery and DBExec. Nil is ErrNoDatabase.
	OnQuery func(sql string, args []any) ([]map[string]any, error)
	OnExec  func(sql string, args []any) (Result, error)
	// FailCache makes every cache call answer 0, as the runtime does on a failure.
	FailCache bool
}

// NewFake is an empty Fake.
func NewFake() *Fake { return &Fake{Counters: map[string]int64{}} }

func (f *Fake) CallerWallet() string { return f.Wallet }

func (f *Fake) CacheIncrBy(key string, delta int64) int64 {
	if f.FailCache {
		return 0
	}
	f.Counters[key] += delta
	return f.Counters[key]
}

func (f *Fake) DBQuery(sql string, args ...any) ([]map[string]any, error) {
	if f.OnQuery == nil {
		return nil, ErrNoDatabase
	}
	return f.OnQuery(sql, args)
}

func (f *Fake) DBExec(sql string, args ...any) (Result, error) {
	if f.OnExec == nil {
		return Result{}, ErrNoDatabase
	}
	return f.OnExec(sql, args)
}

func (f *Fake) LogInfo(msg string) { f.Logs = append(f.Logs, msg) }

// Logged reports whether a log line contains s.
func (f *Fake) Logged(s string) bool {
	for _, l := range f.Logs {
		if strings.Contains(l, s) {
			return true
		}
	}
	return false
}
