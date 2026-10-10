package auth

import (
	"os"
	"testing"
	"time"
)

// TestMain waits out the second this test process started in: these tests sign
// stamps and verify them in the same process, and a nonced stamp from that
// second is refused as possibly older than the process.
func TestMain(m *testing.M) {
	time.Sleep(time.Until(CoordinationStampsAcceptedAfter()))
	os.Exit(m.Run())
}
