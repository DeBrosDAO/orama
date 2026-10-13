// Test cases for consensus.yml, run by `make -C chain semgrep-test`. `ruleid:` marks the line below
// as a finding the rule must report; `ok:` marks a line it must not. The file is never compiled:
// the directory starts with a dot, which the go tool skips.
package keeper

import (
	// ruleid: consensus-random
	"crypto/rand"
	// ruleid: consensus-random
	mrand "math/rand"
	// ruleid: consensus-random
	"math/rand/v2"
	"sort"
	"time"
)

func mapRange(balances map[string]int, ids map[int]struct{}) int {
	total := 0
	// ruleid: consensus-map-iteration
	for addr, v := range balances {
		_ = addr
		total += v
	}
	// ruleid: consensus-map-iteration
	for id := range ids {
		_ = id
	}
	keys := make([]string, 0, len(balances))
	// ruleid: consensus-map-iteration
	for k := range balances {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	// ok: consensus-map-iteration
	for _, k := range keys {
		total += balances[k]
	}
	// ok: consensus-map-iteration
	for i := range 3 {
		total += i
	}
	return total
}

func clock(start time.Time) time.Duration {
	// ruleid: consensus-wall-clock
	now := time.Now()
	// ruleid: consensus-wall-clock
	_ = time.Since(start)
	// ruleid: consensus-wall-clock
	_ = time.Until(start)
	// ok: consensus-wall-clock
	return now.Sub(start)
}

func floats(x int) int {
	// ruleid: consensus-float
	var share float64 = 0.5
	// ruleid: consensus-float
	return int(float64(x) * share)
}

func concurrent(f func()) {
	// ruleid: consensus-goroutine
	go f()
	// ruleid: consensus-goroutine
	go func() { f() }()
	// ok: consensus-goroutine
	f()
}

func random() int {
	b := make([]byte, 1)
	_, _ = rand.Read(b)
	return mrand.Intn(10)
}
