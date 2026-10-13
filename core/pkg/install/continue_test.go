package install

import (
	"os"
	"strings"
	"testing"
)

// These failures used to be printed and then ignored, so an install reported
// success with no rqlite directory, no cluster peer id, or no system
// packages. Restoring the warning puts the old string back.
func TestInstall_doesNotContinueAfterTheseFailures(t *testing.T) {
	for _, file := range []string{"orchestrator.go", "prebuilt.go"} {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, gone := range []string{
			"RQLite initialization warning",
			"Could not save IPFS Cluster peer ID",
			"System dependencies warning",
		} {
			if strings.Contains(string(body), gone) {
				t.Errorf("%s still logs %q and carries on", file, gone)
			}
		}
	}
}
