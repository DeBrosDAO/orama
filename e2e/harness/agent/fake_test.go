package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The fake rw-agent-headless is this test binary, re-run with fakeAgentEnv
// set: TestMain hands it to fakeAgentMain before any test runs.
const (
	fakeAgentEnv = "E2E_FAKE_RW_AGENT"
	fakeModeEnv  = "E2E_FAKE_RW_AGENT_MODE"
	fakeAddress  = "0x1111111111111111111111111111111111111111"
)

// Fake agent modes.
const (
	modeOK         = "ok"
	modeRefuseCap  = "refuse-cap"
	modeNeverReady = "never-ready"
	modeLocked     = "locked"
	modeIgnoreTerm = "ignore-term"
	modeWrongSock  = "wrong-socket"
)

func TestMain(m *testing.M) {
	if os.Getenv(fakeAgentEnv) == "1" {
		os.Exit(fakeAgentMain(os.Args[1:], os.Getenv(fakeModeEnv)))
	}
	if os.Getenv(launcherEnv) != "" {
		os.Exit(launcherMain(os.Getenv(launcherEnv)))
	}
	os.Exit(m.Run())
}

func flagValues(args []string) map[string][]string {
	out := map[string][]string{}
	for i := 0; i+1 < len(args); i += 2 {
		out[args[i]] = append(out[args[i]], args[i+1])
	}
	return out
}

func fakeAgentMain(args []string, mode string) int {
	f := flagValues(args)
	pw := f["--password-file"][0]
	if fi, err := os.Lstat(pw); err != nil || fi.Mode().Perm() != 0o600 {
		fmt.Fprintln(os.Stderr, "error: the password file must be a 0600 regular file")
		return 2
	}
	if mode == modeRefuseCap {
		fmt.Fprintf(os.Stderr, "error: capability %s cannot be pre-approved on a headless agent\n", CapWalletSignArchive)
		return 2
	}
	sock := f["--socket"][0]
	ln, err := net.Listen("unix", sock)
	if err != nil {
		fmt.Fprintln(os.Stderr, "listen:", err)
		return 3
	}
	defer ln.Close()
	_ = os.Chmod(sock, 0o600)
	locked := mode == modeLocked
	go func() {
		_ = http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Like the real agent, log every request: a write to an
			// output nobody reads any more is what kills an agent whose
			// output is a pipe of the process that started it.
			fmt.Println("request", r.Method, r.URL.Path)
			fmt.Fprintln(os.Stderr, "served", r.URL.Path)
			_, _ = fmt.Fprintf(w, `{"ok":true,"data":{"version":"fake","locked":%v,"pid":%d}}`, locked, os.Getpid())
		}))
	}()
	if mode != modeNeverReady {
		writeFakeReady(f["--ready-file"][0], sock, mode)
	}
	return waitForTerm(mode)
}

func writeFakeReady(path, sock, mode string) {
	if mode == modeWrongSock {
		sock = "/elsewhere.sock"
	}
	raw, _ := json.Marshal(readyFile{PID: os.Getpid(), Socket: sock, Address: fakeAddress})
	tmp := path + ".tmp"
	_ = os.WriteFile(tmp, raw, 0o600)
	_ = os.Rename(tmp, path)
}

func waitForTerm(mode string) int {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM)
	for range sigs {
		if mode != modeIgnoreTerm {
			return 0
		}
	}
	return 0
}

// fakeBins writes a fake rw (a shell script) and a wrapper that runs this
// test binary as the agent in mode.
func fakeBins(t *testing.T, mode, rwScript string) (rw, agentBin string) {
	t.Helper()
	dir := t.TempDir()
	rw = filepath.Join(dir, "rw")
	writeScript(t, rw, rwScript)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	agentBin = filepath.Join(dir, "rw-agent-headless")
	writeScript(t, agentBin, fmt.Sprintf("#!/bin/sh\n%s=1 %s=%s exec %q \"$@\"\n", fakeAgentEnv, fakeModeEnv, mode, self))
	return rw, agentBin
}

func writeScript(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// okRW checks what rw init is given and records the wallet.
const okRW = `#!/bin/sh
[ "$1" = init ] && [ "$2" = --mnemonic-file ] || { echo "usage" >&2; exit 2; }
[ -n "$ROOTWALLET_PASSWORD" ] || { echo "no password" >&2; exit 3; }
[ -z "$XDG_DATA_HOME" ] || { echo "XDG_DATA_HOME leaked" >&2; exit 4; }
[ "$(wc -w < "$3" | tr -d ' ')" = 12 ] || { echo "mnemonic is not 12 words" >&2; exit 5; }
mkdir -p "$HOME/.rootwallet" && echo wallet > "$HOME/.rootwallet/wallet.enc"
`

// failingRW refuses to create a wallet.
const failingRW = "#!/bin/sh\necho 'rw: wallet already exists' >&2\nexit 1\n"

// shortBase is a base directory short enough for the agent socket.
func shortBase(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	if len(base) < maxSocketPath-40 {
		return base
	}
	base, err := os.MkdirTemp("/tmp", "e2eat")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })
	return base
}

// fakeHome points the real-home lookup at a temp dir for the test.
func fakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	prev := lookupRealHome
	lookupRealHome = func() (string, error) { return home, nil }
	t.Cleanup(func() { lookupRealHome = prev })
	return home
}

func waitGone(pid int) bool {
	return waitExit(context.Background(), pid, 5*time.Second)
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
