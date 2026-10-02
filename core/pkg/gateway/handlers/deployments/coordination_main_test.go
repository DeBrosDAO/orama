package deployments

import (
	"os"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/auth"
)

// TestMain waits out the second this test process started in: these tests sign
// coordination stamps and verify them in the same process, and a stamp from
// that second is refused as possibly older than the process.
func TestMain(m *testing.M) {
	time.Sleep(time.Until(auth.CoordinationStampsAcceptedAfter()))
	os.Exit(m.Run())
}
