package installers

import (
	"os"
	"strings"
	"testing"
)

// A nameserver whose stub listener stayed up used to install anyway and then
// fail to bind :53. DisableResolvedStubListener has to fail the install.
func TestDisableResolvedStubListener_doesNotContinueAfterAFailure(t *testing.T) {
	body, err := os.ReadFile("coredns.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{
		"Could not remove /etc/resolv.conf",
		"Failed to restart systemd-resolved",
	} {
		if strings.Contains(string(body), gone) {
			t.Errorf("coredns.go still logs %q and carries on", gone)
		}
	}
}
