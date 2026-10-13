package orchardproc

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/chain/x/shielded/bundle"
	"github.com/DeBrosOfficial/network/chain/x/shielded/verify"
)

// The tests run this test binary as the "verifier": with fakeModeFileEnv set, TestMain speaks the
// protocol instead of running tests. The mode file is read on every request, so a test switches
// behaviour without restarting the process.
const fakeModeFileEnv = "ORAMA_FAKE_VERIFIER_MODE_FILE"

// fakeIgnoreEOFEnv makes the fake child outlive the end of its stdin, so a test can tell what else
// ends a child.
const fakeIgnoreEOFEnv = "ORAMA_FAKE_VERIFIER_IGNORE_EOF"

const (
	modeAccept    = "accept"
	modeReject    = "reject"    // code 3, proof rejected
	modeHang      = "hang"      // never answers
	modeDie       = "die"       // exits without answering
	modeWrongID   = "wrong-id"  // answers with another id
	modeBadFrame  = "bad-frame" // answers with a frame of the wrong length
	modeBadCode   = "bad-code"  // answers with an undefined code
	modePanicCode = "panic"     // answers with the panic code
)

// fakeEnv is the whole environment the fake child gets: the verifier's environment is empty by
// default, so the variable that picks the fake mode is passed on purpose.
func fakeEnv() []string { return []string{fakeModeFileEnv + "=" + os.Getenv(fakeModeFileEnv)} }

// parentModeHook lets a platform file run this test binary as a node (pdeathsig_linux_test.go).
var parentModeHook func() (code int, ok bool)

func TestMain(m *testing.M) {
	if parentModeHook != nil {
		if code, ok := parentModeHook(); ok {
			os.Exit(code)
		}
	}
	if modeFile := os.Getenv(fakeModeFileEnv); modeFile != "" {
		os.Exit(runFake(modeFile))
	}
	os.Exit(m.Run())
}

func runFake(modeFile string) int {
	writeFrame(os.Stdout, ReadyID, codeOK)
	for {
		var head [4]byte
		if _, err := io.ReadFull(os.Stdin, head[:]); err != nil {
			if os.Getenv(fakeIgnoreEOFEnv) != "" {
				time.Sleep(time.Hour) // the test holds the parent's pipe closed on purpose: only a signal ends this child
			}
			return 0
		}
		body := make([]byte, binary.BigEndian.Uint32(head[:]))
		if _, err := io.ReadFull(os.Stdin, body); err != nil {
			return 1
		}
		id := binary.BigEndian.Uint64(body[:8])
		raw, _ := os.ReadFile(modeFile)
		switch strings.TrimSpace(string(raw)) {
		case modeAccept:
			writeFrame(os.Stdout, id, codeOK)
		case modeReject:
			writeFrame(os.Stdout, id, codeProofRejected)
		case modeHang:
			time.Sleep(time.Hour) // a bare select{} would end the child at once with a deadlock error
		case modeDie:
			return 3
		case modeWrongID:
			writeFrame(os.Stdout, id+1, codeOK)
		case modeBadFrame:
			_, _ = os.Stdout.Write([]byte{0, 0, 0, 2, 0, 0})
		case modeBadCode:
			writeFrame(os.Stdout, id, 99)
		case modePanicCode:
			writeFrame(os.Stdout, id, codePanic)
			return 0 // like the real child, which ends after answering a panic
		}
	}
}

func writeFrame(w io.Writer, id uint64, code byte) {
	frame := binary.BigEndian.AppendUint32(nil, responseLen)
	frame = binary.BigEndian.AppendUint64(frame, id)
	frame = append(frame, code)
	_, _ = w.Write(frame)
}

// fixture starts a Verifier whose process is this test binary in fake mode.
type fixture struct {
	t        *testing.T
	v        *Verifier
	modeFile string
}

func newFixture(t *testing.T, timeout time.Duration) *fixture {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	modeFile := filepath.Join(t.TempDir(), "mode")
	t.Setenv(fakeModeFileEnv, modeFile)
	f := &fixture{t: t, modeFile: modeFile}
	f.mode(modeAccept)
	f.v = mustNew(t, Config{Path: exe, ChainID: "test-chain", RequestTimeout: timeout, StartTimeout: 10 * time.Second, Env: fakeEnv()})
	t.Cleanup(func() { _ = f.v.Close() })
	return f
}

func (f *fixture) mode(m string) {
	f.t.Helper()
	if err := os.WriteFile(f.modeFile, []byte(m), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

// fakeBundle is framed like a bundle up to the proof, which is all the sighash needs; the fake
// verifier does not look at the rest.
func fakeBundle() []byte {
	b := make([]byte, 1+bundle.ActionLen+bundle.HeaderLen+16)
	b[0] = 1
	return b
}

func TestVerify_acceptAndRejectionMapping(t *testing.T) {
	f := newFixture(t, 5*time.Second)
	if err := f.v.Verify(fakeBundle(), nil); err != nil {
		t.Fatalf("accept: %v", err)
	}
	f.mode(modeReject)
	if err := f.v.Verify(fakeBundle(), nil); !errors.Is(err, verify.ErrProofRejected) {
		t.Fatalf("reject: %v", err)
	}
	f.mode(modePanicCode)
	if err := f.v.Verify(fakeBundle(), nil); !errors.Is(err, verify.ErrVerifierPanic) || !errors.Is(err, verify.ErrTampered) || errors.Is(err, verify.ErrVerifierFault) {
		t.Fatalf("panic code: got %v, want a verdict on the bundle (every node's verifier panics on the same bytes), not a fault", err)
	}
	f.mode(modeBadCode)
	if err := f.v.Verify(fakeBundle(), nil); !errors.Is(err, verify.ErrVerifierFault) {
		t.Fatalf("undefined code: %v", err)
	}
}

func TestVerify_inputBoundsRefusedBeforeTheProcess(t *testing.T) {
	v := mustNew(t, Config{Path: "/nonexistent", ChainID: "c"})
	for name, in := range map[string][]byte{
		"empty":     nil,
		"oversize":  make([]byte, bundle.MaxBytes+1),
		"no header": {1, 2, 3},
	} {
		if err := v.Verify(in, nil); !errors.Is(err, verify.ErrMalformed) {
			t.Errorf("%s: got %v, want ErrMalformed (and no attempt to start the binary)", name, err)
		}
	}
}

func TestVerify_missingBinaryIsNotLinked(t *testing.T) {
	for name, path := range map[string]string{"empty path": "", "missing file": filepath.Join(t.TempDir(), "nope")} {
		err := mustNew(t, Config{Path: path, ChainID: "c"}).Verify(fakeBundle(), nil)
		if !errors.Is(err, verify.ErrVerifierNotLinked) {
			t.Errorf("%s: got %v, want ErrVerifierNotLinked", name, err)
		}
	}
}

// A binary that is not there says where it looked and what to do. Once the node runs, a binary that
// disappeared is a fault of this machine (a block halts on it), while a node never configured with
// one stays "not linked" alone.
func TestVerify_missingBinaryMessageIsActionable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orama-orchard-verifier")
	err := mustNew(t, Config{Path: path, ChainID: "c"}).Verify(fakeBundle(), nil)
	if !errors.Is(err, verify.ErrVerifierNotLinked) || !errors.Is(err, verify.ErrVerifierFault) {
		t.Fatalf("got %v, want ErrVerifierNotLinked and ErrVerifierFault", err)
	}
	for _, want := range []string{path, "--shielded-verifier", "install"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message %q does not mention %q", err, want)
		}
	}
	err = mustNew(t, Config{ChainID: "c"}).Verify(fakeBundle(), nil)
	if !errors.Is(err, verify.ErrVerifierNotLinked) || errors.Is(err, verify.ErrVerifierFault) {
		t.Fatalf("an unconfigured verifier is not linked, and no fault: got %v", err)
	}
}

func TestVerify_timeoutRejectsThenRecovers(t *testing.T) {
	f := newFixture(t, 300*time.Millisecond)
	f.mode(modeHang)
	start := time.Now()
	err := f.v.Verify(fakeBundle(), nil)
	if !errors.Is(err, verify.ErrVerifierFault) || errors.Is(err, verify.ErrTampered) {
		t.Fatalf("hang: got %v, want a fault", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("the timeout took %s", time.Since(start))
	}
	f.mode(modeAccept)
	if err := f.v.Verify(fakeBundle(), nil); err != nil {
		t.Fatalf("after a timeout the next request must run on a fresh process: %v", err)
	}
}

func TestVerify_processDiesMidRequestRejectsThenRecovers(t *testing.T) {
	f := newFixture(t, 5*time.Second)
	f.mode(modeDie)
	if err := f.v.Verify(fakeBundle(), nil); !errors.Is(err, verify.ErrVerifierFault) {
		t.Fatalf("die: got %v", err)
	}
	f.mode(modeAccept)
	if err := f.v.Verify(fakeBundle(), nil); err != nil {
		t.Fatalf("recover: %v", err)
	}
}

func TestVerify_killedFromOutsideMidRequestRejectsThenRecovers(t *testing.T) {
	f := newFixture(t, 30*time.Second)
	f.mode(modeHang)
	errc := make(chan error, 1)
	go func() { errc <- f.v.Verify(fakeBundle(), nil) }()
	// Verify holds the mutex for the whole request, so the process is reached through the
	// process table: kill the child of this test binary.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if killChildren(t) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	select {
	case err := <-errc:
		if !errors.Is(err, verify.ErrVerifierFault) {
			t.Fatalf("killed: got %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a killed verifier did not fail the request")
	}
	f.mode(modeAccept)
	if err := f.v.Verify(fakeBundle(), nil); err != nil {
		t.Fatalf("recover: %v", err)
	}
}

func TestVerify_deadBetweenRequestsIsRestartedNotRejected(t *testing.T) {
	f := newFixture(t, 5*time.Second)
	if err := f.v.Verify(fakeBundle(), nil); err != nil {
		t.Fatal(err)
	}
	f.v.mu.Lock()
	first := f.v.proc
	_ = first.cmd.Process.Kill()
	f.v.mu.Unlock()
	select {
	case <-first.done:
	case <-time.After(5 * time.Second):
		t.Fatal("process did not exit")
	}
	if err := f.v.Verify(fakeBundle(), nil); err != nil {
		t.Fatalf("a process that died while idle must be restarted, not fail a valid bundle: %v", err)
	}
	f.v.mu.Lock()
	defer f.v.mu.Unlock()
	if f.v.proc == first {
		t.Fatal("the dead process was reused")
	}
}

func TestVerify_protocolViolationsAreFaults(t *testing.T) {
	f := newFixture(t, 5*time.Second)
	for _, m := range []string{modeWrongID, modeBadFrame} {
		f.mode(m)
		if err := f.v.Verify(fakeBundle(), nil); !errors.Is(err, verify.ErrVerifierFault) {
			t.Errorf("%s: got %v, want a fault", m, err)
		}
	}
}

func TestCheck_secondVerifierFaultFailsTheBundle(t *testing.T) {
	f := newFixture(t, 300*time.Millisecond)
	f.mode(modeHang)
	err := verify.Check(fakeBundle(), nil, acceptAll{}, f.v)
	if err == nil {
		t.Fatal("a hung second verifier must fail the bundle")
	}
}

type acceptAll struct{}

func (acceptAll) ID() string { return "accept-all" }

func (acceptAll) Verify([]byte, []byte) error { return nil }

func TestVerify_concurrentRequestsAreSerialized(t *testing.T) {
	f := newFixture(t, 5*time.Second)
	errs := make(chan error, 16)
	for i := 0; i < cap(errs); i++ {
		go func() { errs <- f.v.Verify(fakeBundle(), nil) }()
	}
	for i := 0; i < cap(errs); i++ {
		if err := <-errs; err != nil {
			t.Error(err)
		}
	}
}

// mustNew builds a Verifier, pinning the binary to its own hash when it exists (a missing file gets
// an arbitrary pin, so the missing-file error is what a test sees).
func mustNew(t *testing.T, cfg Config) *Verifier {
	t.Helper()
	if raw, err := os.ReadFile(cfg.Path); err == nil && cfg.SHA256 == "" {
		sum := sha256.Sum256(raw)
		cfg.SHA256 = hex.EncodeToString(sum[:])
	}
	v, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestNew_refusesAnEmptyChainID(t *testing.T) {
	if _, err := New(Config{Path: "x", SHA256: "y"}); !errors.Is(err, verify.ErrEmptyChainID) {
		t.Fatalf("got %v", err)
	}
}

func TestVerifierID_isDistinctFromTheLibrarysSoCheckCountsTwo(t *testing.T) {
	v := mustNew(t, Config{Path: "/nonexistent", ChainID: "c"})
	if v.ID() == "" || v.ID() == "orchard" {
		t.Fatalf("id %q", v.ID())
	}
}

// The binary must be the one the node is pinned to: a different file, or no pin, is refused before
// anything is started.
func TestPin_aWrongOrMissingPinRefusesToStart(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(fakeModeFileEnv, filepath.Join(t.TempDir(), "mode"))
	for name, pin := range map[string]string{"missing": "", "wrong": strings.Repeat("0", 64)} {
		v, err := New(Config{Path: exe, ChainID: "c", SHA256: pin})
		if err != nil {
			t.Fatal(err)
		}
		if err := v.Warm(); !errors.Is(err, ErrBinaryPin) {
			t.Errorf("%s pin: Warm got %v, want ErrBinaryPin", name, err)
		}
		if err := v.Verify(fakeBundle(), nil); !errors.Is(err, verify.ErrVerifierNotLinked) {
			t.Errorf("%s pin: Verify got %v, want ErrVerifierNotLinked", name, err)
		}
	}
}

// copyOfTestBinary copies this test binary (which speaks the fake protocol) to a file of its own
// that a test can pin and swap.
func copyOfTestBinary(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "orama-orchard-verifier")
	if err := os.WriteFile(path, raw, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func hashOf(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// The pin is checked on the file that is then started. A file swapped in at the path between the
// check and the start (the old check-then-exec window) is not what runs: the pinned file is.
func TestPin_aBinarySwappedAfterTheCheckIsNotTheOneThatRuns(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("starting the process from the checked file is Linux's /proc/self/fd; elsewhere the window is documented (exec_other.go)")
	}
	path := copyOfTestBinary(t)
	pin := hashOf(t, path)
	marker := filepath.Join(t.TempDir(), "intruder-ran")
	intruder := filepath.Join(t.TempDir(), "intruder")
	script := "#!/bin/sh\ntouch " + marker + "\nexit 1\n"
	if err := os.WriteFile(intruder, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	afterPinCheck = func() {
		if err := os.Rename(intruder, path); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { afterPinCheck = nil })
	t.Setenv(fakeModeFileEnv, filepath.Join(t.TempDir(), "mode"))
	if err := os.WriteFile(os.Getenv(fakeModeFileEnv), []byte(modeAccept), 0o600); err != nil {
		t.Fatal(err)
	}

	v, err := New(Config{Path: path, ChainID: "c", SHA256: pin, StartTimeout: 10 * time.Second, Env: fakeEnv()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = v.Close() })
	if err := v.Warm(); err != nil {
		t.Fatalf("the pinned file must start even though the path now holds another file: %v", err)
	}
	if err := v.Verify(fakeBundle(), nil); err != nil {
		t.Fatalf("the pinned verifier answers: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the file swapped in after the pin check was executed")
	}
	if hashOf(t, path) == pin {
		t.Fatal("the test did not swap the file at the path")
	}
}

// A binary that does not match the pin is refused, whatever sits at the path, and nothing starts.
func TestPin_aMismatchedBinaryIsRefusedAndNothingStarts(t *testing.T) {
	path := copyOfTestBinary(t)
	marker := filepath.Join(t.TempDir(), "ran")
	script := "#!/bin/sh\ntouch " + marker + "\nexit 1\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	v, err := New(Config{Path: path, ChainID: "c", SHA256: strings.Repeat("ab", 32)})
	if err != nil {
		t.Fatal(err)
	}
	err = v.Warm()
	if !errors.Is(err, ErrBinaryPin) || !errors.Is(err, verify.ErrVerifierFault) {
		t.Fatalf("got %v, want ErrBinaryPin", err)
	}
	for _, want := range []string{path, hashOf(t, path), strings.Repeat("ab", 32)} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message %q does not mention %q", err, want)
		}
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a binary that failed the pin was executed")
	}
}

// Only a regular file is a verifier: a directory at the path is refused with the pin error.
func TestPin_aDirectoryIsRefused(t *testing.T) {
	v := mustNew(t, Config{Path: t.TempDir(), ChainID: "c", SHA256: strings.Repeat("0", 64)})
	if err := v.Warm(); err == nil {
		t.Fatal("a directory started")
	}
}

func TestWarm_startsTheProcessBeforeTheFirstBundle(t *testing.T) {
	f := newFixture(t, 5*time.Second)
	if err := f.v.Warm(); err != nil {
		t.Fatal(err)
	}
	f.v.mu.Lock()
	defer f.v.mu.Unlock()
	if f.v.proc == nil {
		t.Fatal("Warm did not start the process")
	}
}

// A FIFO at the path must not hang the open: Warm answers at once, with the pin error.
func TestPin_aFifoAtThePathIsRefusedWithoutBlocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orama-orchard-verifier")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("no fifo here: %v", err)
	}
	v, err := New(Config{Path: path, ChainID: "c", SHA256: strings.Repeat("0", 64)}) // mustNew would read the fifo itself
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- v.Warm() }()
	select {
	case err := <-done:
		if !errors.Is(err, ErrBinaryPin) || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("got %v, want the pin error naming a non-regular file", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("opening a fifo as the verifier binary hung")
	}
}

// A link at the file itself is not followed: the path must be the file.
func TestPin_aSymlinkAtTheFileIsRefused(t *testing.T) {
	real := copyOfTestBinary(t)
	link := filepath.Join(t.TempDir(), "orama-orchard-verifier")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	v := mustNew(t, Config{Path: link, ChainID: "c", SHA256: hashOf(t, real)})
	if err := v.Warm(); !errors.Is(err, ErrBinaryPin) || !errors.Is(err, verify.ErrVerifierFault) {
		t.Fatalf("got %v, want ErrBinaryPin", err)
	}
}

// A link in a directory above the file is fine: cosmovisor's current is one.
func TestPin_aLinkedDirectoryAboveTheFileIsFollowed(t *testing.T) {
	dir := t.TempDir()
	real := copyOfTestBinary(t)
	t.Setenv(fakeModeFileEnv, filepath.Join(t.TempDir(), "mode"))
	if err := os.WriteFile(os.Getenv(fakeModeFileEnv), []byte(modeAccept), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(real), filepath.Join(dir, "current")); err != nil {
		t.Fatal(err)
	}
	v := mustNew(t, Config{Path: filepath.Join(dir, "current", filepath.Base(real)), ChainID: "c", SHA256: hashOf(t, real), StartTimeout: 10 * time.Second, Env: fakeEnv()})
	t.Cleanup(func() { _ = v.Close() })
	if err := v.Warm(); err != nil {
		t.Fatalf("a path through a linked directory must start: %v", err)
	}
}

// A script is a valid verifier too (its interpreter opens the checked file after the exec): the
// fake that the app tests use is one.
func TestPin_aScriptStartsFromTheCheckedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orama-orchard-verifier")
	ready := "#!/bin/sh\nprintf '\\000\\000\\000\\011\\377\\377\\377\\377\\377\\377\\377\\377\\000'\nhead -c 1 >/dev/null\n"
	if err := os.WriteFile(path, []byte(ready), 0o755); err != nil {
		t.Fatal(err)
	}
	v := mustNew(t, Config{Path: path, ChainID: "c", StartTimeout: 10 * time.Second})
	t.Cleanup(func() { _ = v.Close() })
	if err := v.Warm(); err != nil {
		t.Fatalf("a script verifier did not become ready: %v", err)
	}
}

// A verifier that panicked on a bundle is not asked again: the verdict is the panic's, the
// process is stopped, and the next bundle runs on a fresh one.
func TestVerify_afterAPanicTheNextRequestRunsOnAFreshProcess(t *testing.T) {
	f := newFixture(t, 5*time.Second)
	if err := f.v.Verify(fakeBundle(), nil); err != nil {
		t.Fatal(err)
	}
	first := f.v.proc
	f.mode(modePanicCode)
	if err := f.v.Verify(fakeBundle(), nil); !errors.Is(err, verify.ErrVerifierPanic) {
		t.Fatalf("got %v, want ErrVerifierPanic", err)
	}
	f.v.mu.Lock()
	stopped := f.v.proc == nil
	f.v.mu.Unlock()
	if !stopped {
		t.Fatal("the process that panicked was kept")
	}
	select {
	case <-first.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the process that panicked is still running")
	}
	f.mode(modeAccept)
	if err := f.v.Verify(fakeBundle(), nil); err != nil {
		t.Fatalf("the request after a panic must run on a fresh process: %v", err)
	}
}

// The answer of a child that exits right after writing it must always be read: a panic is a
// verdict, and losing the frame to a closed pipe would turn it into a fault that halts the node.
// Many rounds, the exit racing the read as hard as the scheduler allows.
func TestVerify_aPanicAnswerIsNeverLostToTheChildExiting(t *testing.T) {
	f := newFixture(t, 10*time.Second)
	rounds := 150
	if testing.Short() {
		rounds = 20
	}
	for i := 0; i < rounds; i++ {
		f.mode(modeAccept)
		if err := f.v.Verify(fakeBundle(), nil); err != nil {
			t.Fatalf("round %d: %v", i, err)
		}
		f.mode(modePanicCode)
		runtime.Gosched()
		err := f.v.Verify(fakeBundle(), nil)
		if !errors.Is(err, verify.ErrVerifierPanic) || errors.Is(err, verify.ErrVerifierFault) {
			t.Fatalf("round %d: got %v, want the verdict ErrVerifierPanic every time", i, err)
		}
	}
}

// A relative path with no slash is the file in the working directory, not a PATH lookup.
func TestStart_aPathWithoutASlashIsNotLookedUpOnPATH(t *testing.T) {
	dir := t.TempDir()
	copyTo := filepath.Join(dir, "verifier")
	raw, err := os.ReadFile(copyOfTestBinary(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(copyTo, raw, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv(fakeModeFileEnv, filepath.Join(t.TempDir(), "mode"))
	if err := os.WriteFile(os.Getenv(fakeModeFileEnv), []byte(modeAccept), 0o600); err != nil {
		t.Fatal(err)
	}
	v, err := New(Config{Path: "verifier", ChainID: "c", SHA256: hashOf(t, copyTo), StartTimeout: 10 * time.Second, Env: fakeEnv()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = v.Close() })
	if err := v.Warm(); err != nil {
		t.Fatalf("a relative path must start the file in the working directory: %v", err)
	}
}

// The child gets an empty environment: nothing of the node's (keys, tokens, paths) reaches it.
func TestStart_theChildInheritsNothingFromTheNodesEnvironment(t *testing.T) {
	t.Setenv("ORAMA_SECRET_FOR_THE_TEST", "hunter2")
	path := filepath.Join(t.TempDir(), "orama-orchard-verifier")
	out := filepath.Join(t.TempDir(), "env")
	script := "#!/bin/sh\nenv > " + out + "\nprintf '\\000\\000\\000\\011\\377\\377\\377\\377\\377\\377\\377\\377\\000'\nhead -c 1 >/dev/null\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	v := mustNew(t, Config{Path: path, ChainID: "c", StartTimeout: 10 * time.Second})
	t.Cleanup(func() { _ = v.Close() })
	if err := v.Warm(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "hunter2") || strings.Contains(string(raw), "ORAMA_SECRET") {
		t.Fatalf("the child saw the node's environment:\n%s", raw)
	}
}

// Closing the verifier ends the child: stdin closes, the child reads EOF and exits, which is also
// how it ends when the node itself dies.
func TestClose_endsTheChild(t *testing.T) {
	f := newFixture(t, 5*time.Second)
	if err := f.v.Warm(); err != nil {
		t.Fatal(err)
	}
	f.v.mu.Lock()
	child := f.v.proc
	f.v.mu.Unlock()
	if err := f.v.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-child.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the child outlived Close")
	}
}

// VerifyRunning never starts a process and never waits: with none running it answers ErrNotRunning.
func TestVerifyRunning_neverStartsAProcess(t *testing.T) {
	f := newFixture(t, 5*time.Second)
	if err := f.v.VerifyRunning(fakeBundle(), nil); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("before any start: got %v, want ErrNotRunning", err)
	}
	f.v.mu.Lock()
	started := f.v.proc != nil
	f.v.mu.Unlock()
	if started {
		t.Fatal("VerifyRunning started a process")
	}
	if err := f.v.Warm(); err != nil {
		t.Fatal(err)
	}
	if err := f.v.VerifyRunning(fakeBundle(), nil); err != nil {
		t.Fatalf("with a running process: %v", err)
	}
}

// A child that exited while idle is not started again by VerifyRunning.
func TestVerifyRunning_aChildThatExitedWhileIdleIsNotRestarted(t *testing.T) {
	f := newFixture(t, 5*time.Second)
	if err := f.v.Warm(); err != nil {
		t.Fatal(err)
	}
	f.v.mu.Lock()
	first := f.v.proc
	_ = first.cmd.Process.Kill()
	f.v.mu.Unlock()
	select {
	case <-first.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the process did not exit")
	}
	if err := f.v.VerifyRunning(fakeBundle(), nil); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("got %v, want ErrNotRunning", err)
	}
	f.v.mu.Lock()
	same := f.v.proc == first
	f.v.mu.Unlock()
	if !same {
		t.Fatal("VerifyRunning replaced the dead process")
	}
}

// A request in flight or a start under way is waited for a short time, and then VerifyRunning says
// "busy", which is not "gone": the process may be alive.
func TestVerifyRunning_aBusyVerifierIsBusyNotGone(t *testing.T) {
	f := newFixture(t, 5*time.Second)
	if err := f.v.Warm(); err != nil {
		t.Fatal(err)
	}
	f.v.mu.Lock()
	start := time.Now()
	done := make(chan error, 1)
	go func() { done <- f.v.VerifyRunning(fakeBundle(), nil) }()
	select {
	case err := <-done:
		if !errors.Is(err, ErrBusy) || errors.Is(err, ErrNotRunning) {
			t.Errorf("got %v, want ErrBusy and not ErrNotRunning", err)
		}
		if took := time.Since(start); took < DefaultBusyWait/2 || took > 3*time.Second {
			t.Errorf("waited %s, want about %s", took, DefaultBusyWait)
		}
	case <-time.After(5 * time.Second):
		t.Error("VerifyRunning waited without bound for the lock")
	}
	f.v.mu.Unlock()
	if err := f.v.VerifyRunning(fakeBundle(), nil); err != nil {
		t.Fatalf("once the verifier is free it answers: %v", err)
	}
}

// A request that finishes inside the wait is waited for and answered.
func TestVerifyRunning_waitsForARequestThatFinishesInTime(t *testing.T) {
	f := newFixture(t, 5*time.Second)
	if err := f.v.Warm(); err != nil {
		t.Fatal(err)
	}
	f.v.mu.Lock()
	go func() { time.Sleep(30 * time.Millisecond); f.v.mu.Unlock() }()
	if err := f.v.VerifyRunning(fakeBundle(), nil); err != nil {
		t.Fatalf("got %v, want the answer", err)
	}
}
