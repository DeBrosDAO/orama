package push

import (
	"encoding/base64"
	"os/exec"
	"strings"
	"testing"
)

const testSum = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

var testSigner = "0x" + strings.Repeat("ab", 20)

// decodeStage returns the script an archive-CLI stage command pipes to bash.
func decodeStage(t *testing.T, cmd string) string {
	t.Helper()
	fields := strings.Fields(cmd)
	if len(fields) < 3 || fields[0] != "printf" {
		t.Fatalf("unexpected command shape: %s", cmd)
	}
	script, err := base64.StdEncoding.DecodeString(fields[2])
	if err != nil {
		t.Fatalf("decode the script: %v", err)
	}
	return string(script)
}

// A 0.122.x node has no `node stage-archive`: a push with --trust-signers
// stages with the archive's own CLI, and only after checking that CLI against
// the checksum the verified manifest lists.
func TestNodeStager_trustStagesWithTheArchivesCheckedCLI(t *testing.T) {
	s := &nodeStager{trust: []string{testSigner}, cliSum: testSum}
	cmd := s.stageAndRemove("sudo ", "/tmp/orama-push.Ab12Cd34")
	if strings.ContainsAny(cmd, `'"`) {
		t.Errorf("the command carries quotes, which break inside the fanout's ssh '...': %s", cmd)
	}
	if !strings.HasSuffix(cmd, "| sudo bash -s") {
		t.Errorf("not run as root: %s", cmd)
	}
	script := decodeStage(t, cmd)
	regular := strings.Index(script, `[ ! -L "$cli/bin/orama" ]`)
	check := strings.Index(script, "sha256sum -c")
	if regular < 0 || regular > check {
		t.Errorf("the extracted CLI must be a regular file before its checksum is taken:\n%s", script)
	}
	stage := strings.Index(script, `node stage-archive --archive /tmp/orama-push.Ab12Cd34/archive.tar.gz --trust-signers `+testSigner)
	if check < 0 || stage < 0 || check > stage {
		t.Errorf("the CLI must be checked before it runs:\n%s", script)
	}
	for _, want := range []string{testSum, "/opt/orama/" + SetupCLIPrefix, "rm -rf \"$cli\" /tmp/orama-push.Ab12Cd34",
		"-perm -020 -o -perm -002", "cannot inspect /opt/orama", `[ ! -L "$cli/bin/orama" ]`, "set -eu"} {
		if !strings.Contains(script, want) {
			t.Errorf("script lacks %q:\n%s", want, script)
		}
	}
	if strings.Contains(script, NodeOramaBinary) {
		t.Errorf("the node's installed CLI is used:\n%s", script)
	}
}

// Without --trust-signers nothing from the archive runs: the node's installed
// CLI stages it.
func TestNodeStager_withoutTrustUsesTheInstalledCLI(t *testing.T) {
	s := &nodeStager{}
	if got := s.stage("", "/tmp/orama-push.Ab12Cd34/archive.tar.gz"); got != stageCommand("", "/tmp/orama-push.Ab12Cd34/archive.tar.gz", nil) {
		t.Errorf("stage = %s", got)
	}
	if got := s.stageAndRemove("", "/tmp/orama-push.Ab12Cd34"); got != stageAndRemove("", "/tmp/orama-push.Ab12Cd34", nil) {
		t.Errorf("stageAndRemove = %s", got)
	}
}

// The hub keeps its upload for the fanout: its stage removes only the CLI.
func TestNodeStager_hubKeepsItsUpload(t *testing.T) {
	s := &nodeStager{trust: []string{testSigner}, cliSum: testSum}
	script := decodeStage(t, s.stage("", "/tmp/orama-push.Ab12Cd34/archive.tar.gz"))
	if !strings.Contains(script, "trap 'rm -rf \"$cli\"' EXIT") {
		t.Errorf("the hub's stage removes more than its CLI:\n%s", script)
	}
}

func TestNewNodeStager_refusesWhatIsNotASigner(t *testing.T) {
	if _, _, err := newNodeStager("/nonexistent.tar.gz", []string{"0xnot; rm -rf /"}); err == nil {
		t.Fatal("a malformed signer was accepted")
	}
	if _, _, err := newNodeStager("/nonexistent.tar.gz", []string{testSigner}); err == nil {
		t.Fatal("an archive that does not exist was verified")
	}
}

// The script parses as shell.
func TestArchiveCLIStage_isValidShell(t *testing.T) {
	script := decodeStage(t, archiveCLIStage("", "/tmp/orama-push.Ab12Cd34/archive.tar.gz", testSum, []string{testSigner}, "/tmp/orama-push.Ab12Cd34"))
	if out, err := exec.Command("bash", "-n", "-c", script).CombinedOutput(); err != nil {
		t.Fatalf("not valid shell: %v\n%s\n%s", err, out, script)
	}
}
