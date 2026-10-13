package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeSystemctl puts a systemctl on PATH that records its arguments, one call
// per line, and returns where it records them.
func fakeSystemctl(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	script := "#!/bin/sh\necho \"$*\" >> " + log + "\n"
	if err := os.WriteFile(filepath.Join(dir, "systemctl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func calls(t *testing.T, log string) []string {
	t.Helper()
	b, err := os.ReadFile(log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// An upgrade enables orama-node in Phase 5 and starts it once, in its restart
// step; enabling it must not start it, or the node's whole stack bounces twice
// within seconds (stagenet 2026-10-04).
func TestEnableNode_doesNotStartTheNode(t *testing.T) {
	log := fakeSystemctl(t)
	ps := &ProductionSetup{serviceController: NewSystemdController()}
	if err := ps.enableNode(); err != nil {
		t.Fatal(err)
	}
	for _, c := range calls(t, log) {
		if strings.HasPrefix(c, "restart") || strings.HasPrefix(c, "start") {
			t.Errorf("enabling the node ran systemctl %s", c)
		}
	}
	if got := calls(t, log); len(got) == 0 || !strings.HasPrefix(got[0], "enable "+nodeServiceName) {
		t.Errorf("orama-node was not enabled: %v", got)
	}
}

func TestStartNode_restartsTheSupervisorOnce(t *testing.T) {
	log := fakeSystemctl(t)
	ps := &ProductionSetup{serviceController: NewSystemdController()}
	if err := ps.startNode(); err != nil {
		t.Fatal(err)
	}
	if got := calls(t, log); len(got) != 1 || got[0] != "restart "+nodeServiceName {
		t.Fatalf("systemctl calls %v, want one restart of %s", got, nodeServiceName)
	}
}

func TestStartNode_aFailedStartIsAnError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "systemctl"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	ps := &ProductionSetup{serviceController: NewSystemdController()}
	if err := ps.startNode(); err == nil {
		t.Fatal("a systemctl that fails was reported as a started node")
	}
}
