package harness

import (
	"fmt"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"

	"github.com/DeBrosOfficial/network/e2e/harness/runctx"
)

// InterruptedMessage fails a test that asks for the fleet after the run was
// interrupted.
const InterruptedMessage = "the run was interrupted: not starting a test that would disturb the fleet"

// interrupted is set once the package received SIGINT or SIGTERM.
var interrupted atomic.Bool

// watchInterrupt keeps SIGINT and SIGTERM from killing the test binary. The
// runner signals a cancelled stage's whole process group; dying there would
// skip every t.Cleanup, leaving namespaces, partitions and stopped units
// behind. Instead running tests finish (their cleanups restore the fleet) and
// tests that have not reached harness.Fleet yet fail at once. The run-wide
// context (runctx) is cancelled, so the harness's waits in running tests
// stop and those tests reach their cleanups. The runner kills the group when
// its grace period ends.
func watchInterrupt() func() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case sig := <-ch:
				interrupted.Store(true)
				runctx.Cancel()
				fmt.Fprintf(os.Stderr, "e2e: %s received: running tests finish and clean up, no new test starts\n", sig)
			case <-done:
				return
			}
		}
	}()
	return func() { signal.Stop(ch); close(done) }
}
