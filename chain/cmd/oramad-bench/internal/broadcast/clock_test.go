package broadcast

import "github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/ratelimit"

// ratelimitSystem is the wall clock, for tests that need real time to pass.
type ratelimitSystem = ratelimit.SystemClock
