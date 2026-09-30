package oramacli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/pace"
)

// fakeDeviceCLI prints what core's deviceLogin prints, polls once, then
// blocks on $FIFO until `auth approve` writes "ok" or "deny" into it.
const fakeDeviceCLI = `#!/bin/sh
case "$1 $2" in
"auth login")
  echo "There is no wallet on this machine, so the login moves to one that has it."
  echo
  echo "    Your code:  WXYZ-2345"
  echo
  echo "  On a machine where RootWallet is running, run:"
  echo
  echo "    orama auth approve WXYZ-2345"
  echo
  printf "  This code is good for 10m0s. Waiting."
  read answer < "$FIFO"
  printf ".\n"
  [ "$answer" = ok ] || { echo "the approver refused this login" >&2; exit 3; }
  echo "Authentication successful."
  ;;
"auth approve")
  if [ "$4" = "--deny" ] || [ "$6" = "--deny" ]; then echo deny > "$FIFO"; else echo ok > "$FIFO"; fi
  echo "approved $3 $5"
  ;;
esac
`

// deviceRunners are a paced NoWallet runner and an approver sharing a fake
// CLI and a FIFO.
func deviceRunners(t *testing.T, credBurst int) (*Runner, *Runner, *clock) {
	t.Helper()
	base, clk := pacedRunner(t, credBurst, 1)
	if err := os.WriteFile(base.Bin, []byte(fakeDeviceCLI), 0o755); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(t.TempDir(), "approval")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	base.Env = []string{"FIFO=" + fifo}
	writeEnvironments(t, base.Home)
	return base.NoWallet(t), base, clk
}

func writeEnvironments(t *testing.T, home string) {
	t.Helper()
	dir := filepath.Join(home, ConfigDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, EnvironmentsFile), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDeviceLogin_approvedAndPaced(t *testing.T) {
	waiting, approver, clk := deviceRunners(t, 5)
	login := DeviceLogin(t, waiting, "alpha")
	if login.UserCode != "WXYZ-2345" {
		t.Fatalf("code %q", login.UserCode)
	}
	if out := login.Approve(t, approver).Stdout; !strings.Contains(out, "approved WXYZ-2345 alpha") {
		t.Fatalf("approve ran with %q", out)
	}
	if res := login.Wait(t); res.Exit != 0 || !strings.Contains(res.Stdout, "Authentication successful") {
		t.Fatalf("res %+v", res)
	}
	// device start 1 + two polls + approve 2 = the whole burst of 5, no wait.
	if clk.total() != 0 {
		t.Fatalf("slept %s within the burst", clk.total())
	}
	ctx := context.Background()
	if err := approver.Pacer.Wait(ctx, testGatewayHost, pace.ChallengeBucket(approver.Wallet)); err != nil || clk.total() != time.Minute {
		t.Fatalf("the approval's challenge was not paced (slept %s, err %v)", clk.total(), err)
	}
	// The address bucket was empty at the start of that minute: it earned one
	// token, and the next takes another minute.
	for i := 0; i < 2; i++ {
		if err := approver.Pacer.Wait(ctx, testGatewayHost, pace.BucketCred); err != nil {
			t.Fatal(err)
		}
	}
	if clk.total() != 2*time.Minute {
		t.Fatalf("the login's calls were not all charged (slept %s)", clk.total())
	}
}

func TestDeviceLogin_deniedEndsNonZero(t *testing.T) {
	waiting, approver, _ := deviceRunners(t, 10)
	login := DeviceLogin(t, waiting, "")
	login.Deny(t, approver)
	if res := login.Wait(t); res.Exit != 3 || !strings.Contains(res.Stderr, "refused") {
		t.Fatalf("res %+v", res)
	}
}

func TestDeviceLogin_requiresNoWalletRunner(t *testing.T) {
	_, approver, _ := deviceRunners(t, 10)
	tb := &fatalTB{TB: t}
	done := make(chan struct{})
	go func() { defer close(done); DeviceLogin(tb, approver, "") }()
	<-done
	if !strings.Contains(tb.msg, "NoWallet") {
		t.Fatalf("message %q", tb.msg)
	}
}

func TestDeviceLogin_exitWithoutCodeFails(t *testing.T) {
	waiting, _, _ := deviceRunners(t, 10)
	if err := os.WriteFile(waiting.Bin, []byte("#!/bin/sh\necho 'no gateway'\nexit 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	tb := &fatalTB{TB: t}
	done := make(chan struct{})
	go func() { defer close(done); DeviceLogin(tb, waiting, "") }()
	<-done
	if !strings.Contains(tb.msg, "before printing a user code") || !strings.Contains(tb.msg, "no gateway") {
		t.Fatalf("message %q", tb.msg)
	}
}

func TestNoWallet_absentSocketInsideIsolatedHome(t *testing.T) {
	_, approver, _ := deviceRunners(t, 10)
	nw := approver.NoWallet(t)
	if !strings.HasPrefix(nw.AgentSock, nw.Home+string(filepath.Separator)) || nw.Home == approver.Home || nw.Wallet != "" {
		t.Fatalf("runner %+v", nw)
	}
	if err := nw.Check(); err != nil {
		t.Fatalf("no-wallet runner refused: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(nw.AgentSock), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nw.AgentSock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := nw.Check(); err == nil {
		t.Fatal("a no-wallet runner whose socket exists was accepted")
	}
}

func TestParseLoginLine_forms(t *testing.T) {
	code, done, err := parseLoginLine("", "    Your code:  AB-12")
	if code != "AB-12" || done || err != nil {
		t.Fatalf("%q %v %v", code, done, err)
	}
	if _, _, err := parseLoginLine("", "Your code:   "); err == nil {
		t.Fatal("empty code accepted")
	}
	if _, _, err := parseLoginLine("AB-12", "orama auth approve CD-34"); err == nil {
		t.Fatal("mismatched approve line accepted")
	}
	if c, done, _ := parseLoginLine("", "orama auth approve AB-12"); done || c != "" {
		t.Fatal("approve line before the code finished the parse")
	}
	if c, done, err := parseLoginLine("AB-12", "  orama auth approve AB-12"); !done || c != "AB-12" || err != nil {
		t.Fatalf("%q %v %v", c, done, err)
	}
	if args := (&PendingLogin{UserCode: "AB-12", Namespace: "n"}).ApproveArgs(); strings.Join(args, " ") != "auth approve AB-12 --namespace n" {
		t.Fatalf("args %v", args)
	}
}
