package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/broker"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/secrets"
)

// Helper modes of the test binary: it re-runs itself as the process under
// test.
const (
	envHelper        = "E2E_FLEET_TEST_HELPER"
	helperSeal       = "seal"
	helperDispatch   = "dispatch"
	testHCloudSecret = "hc-sealed-5f1e9d3c7b"
	testCFSecret     = "cf-sealed-8a2b4c6d0e"
)

// sealReport is what the seal helper prints after sealing.
type sealReport struct {
	Environ []string `json:"environ"`
	Sealed  string   `json:"sealed"`
	ProcEnv string   `json:"proc_env"`
}

func TestMain(m *testing.M) {
	switch os.Getenv(envHelper) {
	case helperSeal:
		runSealHelper()
	case helperDispatch:
		os.Exit(dispatch(context.Background(), flagFreeArgs()))
	}
	os.Exit(m.Run())
}

// flagFreeArgs are the helper's arguments after "--".
func flagFreeArgs() []string {
	for i, a := range os.Args {
		if a == "--" {
			return os.Args[i+1:]
		}
	}
	return nil
}

func runSealHelper() {
	if err := sealProcess("run"); err != nil {
		os.Stderr.WriteString(err.Error())
		os.Exit(1)
	}
	rep := sealReport{Environ: os.Environ(), Sealed: secrets.Getenv("HCLOUD_TOKEN")}
	if raw, err := os.ReadFile("/proc/self/environ"); err == nil {
		rep.ProcEnv = string(raw)
	}
	if err := json.NewEncoder(os.Stdout).Encode(rep); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

// TestSealProcess_reexecDropsSecrets: `run` re-executes itself; afterwards
// its environ (what children inherit, and /proc/self/environ on Linux) has
// no secret, and the secret is still readable through secrets.LookupEnv.
func TestSealProcess_reexecDropsSecrets(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), envHelper+"="+helperSeal, "HCLOUD_TOKEN="+testHCloudSecret, "INFISICAL_TOKEN=inf-sealed-123456")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("seal helper: %v", err)
	}
	var rep sealReport
	if err := json.Unmarshal(out, &rep); err != nil {
		t.Fatalf("seal helper printed %q: %v", out, err)
	}
	joined := strings.Join(rep.Environ, "\n") + rep.ProcEnv
	for _, leak := range []string{testHCloudSecret, "inf-sealed-123456", envSealedFD} {
		if strings.Contains(joined, leak) {
			t.Fatalf("%q is still in the re-executed runner's environment", leak)
		}
	}
	if rep.Sealed != testHCloudSecret {
		t.Fatalf("the sealed value was lost: %q", rep.Sealed)
	}
	if runtime.GOOS == "linux" && rep.ProcEnv == "" {
		t.Fatal("/proc/self/environ was not read")
	}
}

func TestLoadSealed_refusesABadFD(t *testing.T) {
	for _, v := range []string{"x", "-1", "2"} {
		if err := loadSealed(v); err == nil {
			t.Errorf("fd %q accepted", v)
		}
	}
}

func TestSealProcess_otherCommandsUntouched(t *testing.T) {
	t.Setenv("HCLOUD_TOKEN", testHCloudSecret)
	if err := sealProcess("coverage"); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("HCLOUD_TOKEN") != testHCloudSecret {
		t.Fatal("a command that starts no feature was sealed")
	}
}

func TestBrokerChildEnv_noSecretsAndOwnState(t *testing.T) {
	env := brokerChildEnv([]string{"PATH=/bin", "HCLOUD_TOKEN=" + testHCloudSecret, "INFISICAL_X=y",
		"E2E_FLEET_STATE=/other/state.json", broker.EnvSock + "=/x.sock", "CF_ZONE=dbrsteting.bid"}, "/w/state.json")
	got := strings.Join(env, "\n")
	if strings.Contains(got, testHCloudSecret) || strings.Contains(got, "INFISICAL") || strings.Contains(got, broker.EnvSock) ||
		strings.Contains(got, "/other/state.json") || !strings.Contains(got, "E2E_FLEET_STATE=/w/state.json") ||
		!strings.Contains(got, "CF_ZONE=dbrsteting.bid") {
		t.Fatalf("broker child env %q", got)
	}
}

// TestStartBrokerChild_credentialsOverThePipeOnly starts the real broker
// child (this test binary in dispatch mode): its environment carries no
// credential, yet it redacts both, so it received them over the pipe.
func TestStartBrokerChild_credentialsOverThePipeOnly(t *testing.T) {
	statePath := brokerTestState(t)
	t.Setenv(envHelper, helperDispatch)
	t.Setenv("CF_ZONE", "dbrsteting.bid")
	if err := secrets.Seal(map[string]string{"HCLOUD_TOKEN": testHCloudSecret, "CF_API_TOKEN": testCFSecret}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { secrets.Unseal("HCLOUD_TOKEN", "CF_API_TOKEN") })
	b, err := startBrokerChildArgs(context.Background(), statePath, []string{"-test.run=^$", "--", cmdBrokerServeName})
	if err != nil {
		t.Fatal(err)
	}
	env := strings.Join(b.cmd.Env, "\n")
	if strings.Contains(env, testHCloudSecret) || strings.Contains(env, testCFSecret) {
		t.Fatal("a credential is in the broker child's environment")
	}
	if runtime.GOOS == "linux" {
		raw, _ := os.ReadFile(filepath.Join("/proc", strconv.Itoa(b.cmd.Process.Pid), "environ"))
		if strings.Contains(string(raw), testCFSecret) {
			t.Fatal("a credential is in the broker child's /proc environ")
		}
	}
	c, err := broker.New(b.path)
	if err != nil {
		t.Fatal(err)
	}
	err = c.SetTXT(context.Background(), "x"+testCFSecret+".example.com", "v")
	// The client quotes its own request name; the broker's answer follows.
	_, answer, _ := strings.Cut(fmt.Sprint(err), ".example.com: ")
	if err == nil || strings.Contains(answer, testCFSecret) || !strings.Contains(answer, secrets.Mask) {
		t.Fatalf("the child did not redact the piped credential: %v", err)
	}
	if err := b.stop(); err != nil {
		t.Fatal(err)
	}
}

// brokerTestState writes a guarded state in a short temp dir (the socket
// path must fit sun_path).
func brokerTestState(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "bc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "state.json")
	st := &fleet.State{RunID: "ab12", Env: "e2e-ab12", BaseDomain: "e2e-ab12.dbrsteting.bid", Home: "/tmp/e2e-rw-test0001", RWSock: "/tmp/e2e-rw-test0001/a.sock"}
	if err := st.Save(path); err != nil {
		t.Fatal(err)
	}
	return path
}
