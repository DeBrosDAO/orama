package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rwagent"
)

// launcherEnv makes the test binary a short-lived process that starts an
// agent and exits without stopping it, as `e2e-fleet provision` does.
const launcherEnv = "E2E_FAKE_LAUNCHER"

// launchSpec is what the launcher is told to start, as JSON in launcherEnv.
type launchSpec struct {
	Config  StartConfig `json:"config"`
	Home    string      `json:"home"`
	LogPath string      `json:"log_path"`
}

// launcherMain starts the agent described by spec, prints it and exits.
func launcherMain(spec string) int {
	var s launchSpec
	if err := json.Unmarshal([]byte(spec), &s); err != nil {
		fmt.Fprintln(os.Stderr, "launcher spec:", err)
		return 2
	}
	lookupRealHome = func() (string, error) { return s.Home, nil }
	log, err := os.OpenFile(s.LogPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, "launcher log:", err)
		return 2
	}
	s.Config.Log = log
	a, err := Start(context.Background(), s.Config)
	if err != nil {
		fmt.Fprintln(os.Stderr, "launcher start:", err)
		return 3
	}
	raw, _ := json.Marshal(readyFile{PID: a.PID, Socket: a.Sock, Address: a.Dir})
	fmt.Println(string(raw))
	return 0
}

func TestStart_agentOutlivesTheProcessThatStartedIt(t *testing.T) {
	cfg := startCfg(t, modeOK, okRW)
	logPath := filepath.Join(t.TempDir(), "agent.log")
	spec, err := json.Marshal(launchSpec{Config: cfg, Home: t.TempDir(), LogPath: logPath})
	if err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self)
	cmd.Env = append(os.Environ(), launcherEnv+"="+string(spec))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("the launcher failed: %v: %s", err, stderr.String())
	}
	var started readyFile
	if err := json.Unmarshal(out, &started); err != nil {
		t.Fatalf("launcher output %q: %v", out, err)
	}
	dir := started.Address
	t.Cleanup(func() { _ = StopDir(context.Background(), dir) })
	for i := 0; i < 3; i++ { // each request makes the agent write its output
		if _, err := rwagent.New(started.Socket).Status(context.Background()); err != nil {
			t.Fatalf("request %d: the agent stopped answering once its starter exited: %v", i, err)
		}
	}
	if !alive(started.PID) {
		t.Fatal("the agent died after its starter exited")
	}
	raw, err := os.ReadFile(logPath)
	if err != nil || !bytes.Contains(raw, []byte("request GET /v1/status")) {
		t.Fatalf("the agent's output did not reach its log file: %q %v", raw, err)
	}
	if err := StopDir(context.Background(), dir); err != nil {
		t.Fatalf("StopDir: %v", err)
	}
	if !waitGone(started.PID) {
		t.Fatal("StopDir left the agent running")
	}
}
