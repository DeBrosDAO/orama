package inspector

import (
	"os/exec"
	"strings"
	"testing"
)

// The awk stage of failedUnitsCmd, fed what `systemctl --failed --no-legend
// --plain` prints, yields the unit names.
func TestFailedUnitsCmd_plainOutputGivesUnitNames(t *testing.T) {
	if !strings.Contains(failedUnitsCmd, "--plain") {
		t.Fatalf("failedUnitsCmd lacks --plain, so $1 is systemd's bullet: %s", failedUnitsCmd)
	}
	awk := failedUnitsCmd[strings.Index(failedUnitsCmd, "awk"):]
	cmd := exec.Command("sh", "-c", awk)
	cmd.Stdin = strings.NewReader("cloud-init.service loaded failed failed Cloud-init: Network Stage\n" +
		"orama-namespace-ipfs-cluster@index.service loaded failed failed Orama Namespace IPFS Cluster (index)\n")
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("awk unavailable: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "cloud-init.service\norama-namespace-ipfs-cluster@index.service" {
		t.Fatalf("names = %q", got)
	}
}
