package production

import (
	"fmt"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/deployments/process"
)

// The gateway drives a deployment's unit as the unprivileged orama user. Every
// verb it uses has to be in the sudoers grant, or a tenant's deploy fails on a
// node with a permissions error rather than anything about the deployment.
//
// This is the shape of the bug that made deployment units templates in the
// first place: the gateway wrote units with `tee` into /etc, which the grant
// never covered and which only worked because the gateway was root.
func TestSudoersGrant_coversEveryVerbTheGatewayUses(t *testing.T) {
	grant := sudoersGrant(t)

	for _, verb := range process.SystemctlVerbs {
		want := fmt.Sprintf("systemctl %s %s*", verb, process.UnitPrefix)
		if !strings.Contains(grant, want) {
			t.Errorf("the sudoers grant does not allow %q, which the gateway calls on every deployment", want)
		}
	}
}

// A grant that takes a free-form path is a way to write any file on the node as
// root, because sudo's wildcard matches a slash.
func TestSudoersGrant_takesNoFreeFormPath(t *testing.T) {
	grant := sudoersGrant(t)

	for _, forbidden := range []string{"/usr/bin/tee", "/bin/tee", "/bin/rm", "/usr/bin/rm", "/bin/cp", "ALL=(root) NOPASSWD: ALL"} {
		if strings.Contains(grant, forbidden) {
			t.Errorf("the sudoers grant allows %q, which writes or removes any file on the node as root", forbidden)
		}
	}
}

// sudoersGrant renders the line the provisioner writes.
func sudoersGrant(t *testing.T) string {
	t.Helper()
	rendered := oramaSudoersRule("systemctl", "ufw")
	if !strings.Contains(rendered, "orama ALL=(root) NOPASSWD:") {
		t.Fatalf("this test is not reading the grant: %q", rendered)
	}
	return rendered
}
