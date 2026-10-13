package provision

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

func upForTest(t *testing.T) (*testEnv, *fleet.State) {
	t.Helper()
	e := newTestEnv(t)
	st, err := up(context.Background(), e.cfg, &testLogger{}, e.d)
	if err != nil {
		t.Fatal(err)
	}
	return e, st
}

func TestAddExtra_createsPinsAndRecords(t *testing.T) {
	e, st := upForTest(t)
	n, err := addExtra(context.Background(), st, "extra-1", "hel1", testLimits, e.d)
	if err != nil {
		t.Fatalf("addExtra: %v", err)
	}
	if n.PublicIP == "" || n.ServerID == 0 || n.Location != "hel1" || len(st.Extras) != 1 {
		t.Fatalf("extra %+v, extras %d", n, len(st.Extras))
	}
	raw, _ := os.ReadFile(st.KnownHostsFile)
	if !strings.Contains(string(raw), n.PublicIP+" ") {
		t.Fatal("the extra's host key was not pinned")
	}
}

func TestAddExtra_refusals(t *testing.T) {
	e, st := upForTest(t)
	for _, name := range []string{"Extra", "node-1", "", "x;y", strings.Repeat("a", 30)} {
		if _, err := addExtra(context.Background(), st, name, "nbg1", testLimits, e.d); err == nil {
			t.Errorf("addExtra(%q) was accepted", name)
		}
	}
}

func TestAddExtra_failureAfterCreateDeletesTheServer(t *testing.T) {
	e, st := upForTest(t)
	before, _, _ := e.cloud.counts()
	e.remote.scanErr = errString("sshd never answered")
	if _, err := addExtra(context.Background(), st, "extra-1", "nbg1", testLimits, e.d); err == nil {
		t.Fatal("addExtra succeeded without a host key")
	}
	if after, _, _ := e.cloud.counts(); after != before {
		t.Fatalf("servers %d -> %d: the half-made extra leaked", before, after)
	}
}

func TestAddExtra_runNotUp(t *testing.T) {
	e := newTestEnv(t)
	st := &fleet.State{RunID: "testrun1"}
	if _, err := addExtra(context.Background(), st, "extra-1", "nbg1", testLimits, e.d); err == nil || !strings.Contains(err.Error(), "is it up") {
		t.Fatalf("addExtra with no run: %v", err)
	}
}

func TestRemoveExtra_stateOrphanAndAbsent(t *testing.T) {
	e, st := upForTest(t)
	n, err := addExtra(context.Background(), st, "extra-1", "nbg1", testLimits, e.d)
	if err != nil {
		t.Fatal(err)
	}
	if err := removeExtra(context.Background(), st, "extra-1", e.d); err != nil || len(st.Extras) != 0 {
		t.Fatalf("removeExtra: %v, extras %d", err, len(st.Extras))
	}
	raw, _ := os.ReadFile(st.KnownHostsFile)
	if strings.Contains(string(raw), n.PublicIP+" ") {
		t.Fatal("the removed extra is still pinned")
	}
	if _, err := addExtra(context.Background(), st, "extra-2", "nbg1", testLimits, e.d); err != nil {
		t.Fatal(err)
	}
	st.Extras = nil // lost from the state, still in Hetzner
	before, _, _ := e.cloud.counts()
	if err := removeExtra(context.Background(), st, "extra-2", e.d); err != nil {
		t.Fatal(err)
	}
	if after, _, _ := e.cloud.counts(); after != before-1 {
		t.Fatal("the orphaned extra was not deleted by its name")
	}
	if err := removeExtra(context.Background(), st, "never-there", e.d); err != nil {
		t.Fatalf("removing an absent extra: %v", err)
	}
}

func TestDestroyNode_deletesAndDropsIt(t *testing.T) {
	e, st := upForTest(t)
	victim := st.Nodes[1]
	if err := destroyNode(context.Background(), st, victim.PublicIP, e.d); err != nil {
		t.Fatalf("destroyNode: %v", err)
	}
	if len(st.Nodes) != nodeCount-1 || st.Nodes[1].Name == victim.Name {
		t.Fatalf("nodes after destroy: %+v", st.Nodes)
	}
	if err := destroyNode(context.Background(), st, victim.PublicIP, e.d); err == nil {
		t.Fatal("destroying an unknown host succeeded")
	}
}

func TestRunOnMember_refusals(t *testing.T) {
	e, st := upForTest(t)
	if err := runOnMember(context.Background(), st, "198.51.100.1", breakScript, e.remote, fastTiming()); err == nil {
		t.Fatal("ran on a host outside the fleet")
	}
	st.Nodes[0].SSHUser = "ubuntu"
	if err := runOnMember(context.Background(), st, st.Nodes[0].PublicIP, breakScript, e.remote, fastTiming()); err == nil {
		t.Fatal("ran as a non-root user")
	}
	e.remote.exit, e.remote.stderr = 3, "no staged manifest.sig"
	err := runOnMember(context.Background(), st, st.Nodes[1].PublicIP, breakScript, e.remote, fastTiming())
	if err == nil || !strings.Contains(err.Error(), "no staged") {
		t.Fatalf("a failing script: %v", err)
	}
}

// localScript runs a member script with its node paths moved under dir.
func localScript(t *testing.T, dir, script string) (int, string) {
	t.Helper()
	s := strings.ReplaceAll(script, stagedSignature, filepath.Join(dir, "opt", "manifest.sig"))
	s = strings.ReplaceAll(s, breakBackupDir, filepath.Join(dir, "backup"))
	out, err := exec.Command("sh", "-c", s).CombinedOutput()
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), string(out)
	}
	if err != nil {
		t.Fatal(err)
	}
	return 0, string(out)
}

func TestBreakAndRestoreScripts(t *testing.T) {
	dir := t.TempDir()
	sig := filepath.Join(dir, "opt", "manifest.sig")
	if code, out := localScript(t, dir, breakScript); code != 3 {
		t.Fatalf("break with nothing staged: exit %d %s", code, out)
	}
	if err := os.MkdirAll(filepath.Dir(sig), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sig, []byte("0xgood"), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // the second break is a no-op
		if code, out := localScript(t, dir, breakScript); code != 0 {
			t.Fatalf("break %d: exit %d %s", i, code, out)
		}
	}
	if got, _ := os.ReadFile(sig); strings.TrimSpace(string(got)) != breakMarker {
		t.Fatalf("signature after break: %q", got)
	}
	if code, out := localScript(t, dir, restoreScript); code != 0 {
		t.Fatalf("restore: exit %d %s", code, out)
	}
	if got, _ := os.ReadFile(sig); string(got) != "0xgood" {
		t.Fatalf("signature after restore: %q", got)
	}
	if code, _ := localScript(t, dir, restoreScript); code != 3 {
		t.Fatalf("a second restore: exit %d, want 3", code)
	}
}

func TestRestoreScript_refusesAReStagedSignature(t *testing.T) {
	dir := t.TempDir()
	sig := filepath.Join(dir, "opt", "manifest.sig")
	if err := os.MkdirAll(filepath.Dir(sig), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sig, []byte("0xold"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out := localScript(t, dir, breakScript); code != 0 {
		t.Fatalf("break: %d %s", code, out)
	}
	if err := os.WriteFile(sig, []byte("0xnewpush"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _ := localScript(t, dir, restoreScript); code != 4 {
		t.Fatalf("restore over a re-staged signature: exit %d, want 4", code)
	}
	if got, _ := os.ReadFile(sig); string(got) != "0xnewpush" {
		t.Fatal("restore overwrote the re-staged signature")
	}
}

// testLimits are the extra limits of the test run.
var testLimits = extraLimits{serverType: DefaultServerType, serverLimit: defaultServerLimit, ttl: DefaultTTL}
