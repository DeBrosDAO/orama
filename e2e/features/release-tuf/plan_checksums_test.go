//go:build e2e_fleet

package releasetuf

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tuf"
)

// planInfoPath is the plan's info JSON on the node: what a SoftwareUpgrade proposal carried and
// x/upgrade holds as the plan's info. stage-oramad --upgrade holds the files it stages to the
// checksums it names.
const planInfoPath = workDir + "/plan-info.json"

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// planInfoJSON is cosmovisor's upgrade-info extended with the verifier, naming the given digests
// for linux/amd64 (the platform of the fleet's servers).
func planInfoJSON(oramad, verifier string) string {
	return fmt.Sprintf(`{"binaries":{"linux/amd64":"https://releases.example/oramad?checksum=sha256:%s"},`+
		`"verifiers":{"linux/amd64":"https://releases.example/orama-orchard-verifier?checksum=sha256:%s"}}`, oramad, verifier)
}

// TestStageOramad_aBinaryThePlanDoesNotNameIsRefused offers a correctly signed and verified oramad
// and verifier for an upgrade whose plan names other checksums, or none: each is refused with the
// mismatch named and nothing is staged. The same files staged against the plan that names them
// are placed.
func TestStageOramad_aBinaryThePlanDoesNotNameIsRefused(t *testing.T) {
	nd := newNode(t, "tuf-plan-checksums")
	nd.must(t, "id -u "+chainUser+" >/dev/null 2>&1 || useradd --system --no-create-home --shell /usr/sbin/nologin "+chainUser)
	nd.must(t, "install -d -m 0755 -o root -g root "+chainHome)
	nd.put(t, oramadPath, oramadContent, 0o755)
	nd.put(t, verifierPath, verifierContent, 0o755)
	repo := tuf.New(t)
	valid := nd.upload(t, "oramad-plan", repo.Metadata(t, acceptedVersion, time.Time{}, releaseTargets(oramadContent)))
	nd.adopt(t, repo)

	oramadSum, verifierSum := sha256Hex(oramadContent), sha256Hex(verifierContent)
	other := strings.Repeat("0", 64)
	for _, c := range []struct {
		name, info string
		wants      []string
	}{
		{"another-oramad", planInfoJSON(other, verifierSum), []string{"names an oramad with sha256 " + other[:12], "not staged"}},
		{"another-verifier", planInfoJSON(oramadSum, other), []string{"names an orama-orchard-verifier with sha256 " + other[:12], "not staged"}},
		{"no-checksums", "upgrade to the next release", []string{"carries no upgrade-info JSON"}},
		{"no-verifier", fmt.Sprintf(`{"binaries":{"linux/amd64":"https://releases.example/oramad?checksum=sha256:%s"}}`, oramadSum), []string{"names no orama-orchard-verifier checksum"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			upgrade := "e2e-plan-" + c.name
			nd.put(t, planInfoPath, []byte(c.info), 0o600)
			exit, out := nd.orama(t, oramadArgs(valid, "--upgrade "+upgrade))
			wantRefused(t, exit, out, c.wants...)
			nd.must(t, "test ! -e "+chainHome+"/cosmovisor/upgrades/"+upgrade+"/bin/oramad")
			nd.must(t, "test ! -e "+chainHome+"/cosmovisor/upgrades/"+upgrade+"/bin/orama-orchard-verifier")
		})
	}

	nd.put(t, planInfoPath, []byte(planInfoJSON(oramadSum, verifierSum)), 0o600)
	const upgrade = "e2e-plan-named"
	if exit, out := nd.orama(t, oramadArgs(valid, "--upgrade "+upgrade)); exit != exitOK {
		t.Fatalf("the binaries the plan names were refused (exit %d):\n%s", exit, out)
	}
	nd.must(t, "cmp "+oramadPath+" "+chainHome+"/cosmovisor/upgrades/"+upgrade+"/bin/oramad")
	nd.must(t, "cmp "+verifierPath+" "+chainHome+"/cosmovisor/upgrades/"+upgrade+"/bin/orama-orchard-verifier")
}
