package recover

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// rootWrite matches a shell step that creates, removes, redirects into or
// re-owns a path. As root, each of them follows a symlink the orama user has
// planted in the rqlite data directory.
var rootWrite = regexp.MustCompile(`(?m)(^|[;&|]\s*)(rm|mkdir|mv|cp|chown|chmod|ln|tee|touch)\s|>`)

// rootPart is the script with the argument handed to asOramaUser removed:
// what runs as root.
func rootPart(t *testing.T, script string) string {
	t.Helper()
	i := strings.Index(script, asOramaUser+" sh -c '")
	if i < 0 {
		t.Fatalf("the script never switches to the orama user:\n%s", script)
	}
	rest := script[i+len(asOramaUser+" sh -c "):]
	// The argument is one single-quoted word; ShellQuote writes an embedded
	// quote as '"'"', so the word ends at the first quote not followed by ".
	end := regexp.MustCompile(`'[^"]`).FindAllStringIndex(rest[1:], -1)
	if len(end) == 0 {
		t.Fatalf("unterminated orama-user script:\n%s", script)
	}
	return script[:i] + rest[1+end[0][0]+1:]
}

func assertOnlyOramaWrites(t *testing.T, script string) {
	t.Helper()
	root := rootPart(t, script)
	root = strings.ReplaceAll(root, "2>&1", "")
	if m := rootWrite.FindString(root); m != "" {
		t.Errorf("root writes in the orama tree (%q):\n%s", m, root)
	}
	if strings.Contains(script, "chown") {
		t.Errorf("chown is back: what the orama user creates is already its own:\n%s", script)
	}
}

func assertValidShell(t *testing.T, script string) {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not available")
	}
	if out, err := exec.Command(sh, "-n", "-c", script).CombinedOutput(); err != nil {
		t.Fatalf("not valid shell: %v\n%s\n%s", err, out, script)
	}
}

func TestLeaderResetScript_writesOnlyAsTheOramaUser(t *testing.T) {
	script := leaderResetScript(`[{"id":"10.0.0.1:10101","address":"10.0.0.1:10101","non_voter":false}]`,
		"10.0.0.1:10101", `{"members":["10.0.0.1:10101"]}`)
	assertValidShell(t, script)
	assertOnlyOramaWrites(t, script)
	// The leader's address marker and membership record describe the
	// configuration the recovery installs: a stale marker would read as an
	// address change on the next restart.
	for _, want := range []string{raftDBFile, peersFile, "systemctl is-active --quiet orama-node",
		raftAddrMarkerFile, "10.0.0.1:10101", rqlite.ClusterMembershipPath(rqliteRoot)} {
		if !strings.Contains(script, want) {
			t.Errorf("script lacks %q:\n%s", want, script)
		}
	}
}

func TestFollowerWipeScript_writesOnlyAsTheOramaUser(t *testing.T) {
	recordPath := rqlite.ClusterMembershipPath(rqliteRoot)
	script := followerWipeScript("eyJ9", recordPath)
	assertValidShell(t, script)
	assertOnlyOramaWrites(t, script)
	for _, want := range []string{raftDBFile, raftSubdir, discoveryPeers, rqliteRoot + "/rsnapshots", "systemctl is-active --quiet orama-node",
		"base64 -d > " + recordPath + ".tmp", "mv " + recordPath + ".tmp " + recordPath} {
		if !strings.Contains(script, want) {
			t.Errorf("script lacks %q:\n%s", want, script)
		}
	}
}

// The guard itself: a root rm or redirect outside the orama-user section is
// caught.
func TestAssertOnlyOramaWrites_catchesARootWrite(t *testing.T) {
	for _, bad := range []string{
		"set -e\nrm -f " + raftDBFile + "\n" + asOramaUser + " sh -c 'true'\n",
		"set -e\n" + asOramaUser + " sh -c 'true'\necho x > " + peersFile + "\n",
	} {
		root := rootPart(t, bad)
		if rootWrite.FindString(root) == "" {
			t.Errorf("guard missed a root write in:\n%s", bad)
		}
	}
}
