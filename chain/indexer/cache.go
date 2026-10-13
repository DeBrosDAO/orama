package indexer

import "strconv"

// How long an answer may be cached, by what it depends on. A browser, and a cache between it and the
// gateway, keep a copy for this long, so a crowd of readers of the same page costs the node one read
// per period rather than one per reader.
const (
	// SettledMaxAge is for an answer that cannot change once the index serves it: a block, a
	// transaction by hash, a closed epoch. Ten minutes, not longer: a network that starts again keeps
	// its heights and epoch numbers, and a reader's copy of the old chain must not outlive the reset
	// by much.
	SettledMaxAge = 600

	cacheNoStore = "no-store"
)

// cacheControl is the Cache-Control of a 200 answer to the route with these path segments (after
// /index/v1/). An error answer is never cached (writeError). The gateway passes exactly these two
// forms on (core/pkg/gateway/handlers/chainread/index.go).
func cacheControl(segs []string) string {
	if len(segs) > 0 && segs[0] == operatorSegment {
		return cacheNoStore
	}
	if isSettled(segs) {
		return publicMaxAge(SettledMaxAge)
	}
	return publicMaxAge(HeadMaxAge)
}

// isSettled reports whether the route answers with something fixed once it exists: one block, one
// transaction, or a closed epoch's row, validators or rewards.
func isSettled(segs []string) bool {
	switch {
	case len(segs) == 2 && (segs[0] == "blocks" || segs[0] == "txs"):
		return true
	case len(segs) >= 2 && segs[0] == "epochs":
		return true
	}
	return false
}

func publicMaxAge(seconds int) string { return "public, max-age=" + strconv.Itoa(seconds) }
