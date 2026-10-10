package remotessh

import (
	"encoding/base64"
	"os/exec"
	"strings"
	"testing"
)

func TestScriptCommand_roundTripsTheScriptThroughTheNodesShell(t *testing.T) {
	script := "set -eu\necho \"it's $((1+1))\" 'quoted' $HOME\n"

	cmd := ScriptCommand("", script)

	fields := strings.Fields(cmd)
	got, err := base64.StdEncoding.DecodeString(fields[2])
	if err != nil || string(got) != script {
		t.Fatalf("payload = %q, %v; want the script back", got, err)
	}
	if !strings.HasSuffix(cmd, "| base64 -d | bash -s") {
		t.Errorf("command = %q", cmd)
	}
}

func TestScriptCommand_runsAsRootThroughSudoWhenAsked(t *testing.T) {
	if cmd := ScriptCommand("sudo ", "true\n"); !strings.HasSuffix(cmd, "| base64 -d | sudo bash -s") {
		t.Errorf("command = %q, want the script piped into sudo bash -s", cmd)
	}
}

func TestScriptCommand_executesWithShellMetacharactersIntact(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	out, err := exec.Command(bash, "-c", ScriptCommand("", "printf '%s' \"a b;c\"\n")).Output()
	if err != nil || string(out) != "a b;c" {
		t.Fatalf("ran to %q, %v", out, err)
	}
}
