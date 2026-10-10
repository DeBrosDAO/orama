package orchardproc

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/chain/x/shielded/bundle"
	"github.com/DeBrosOfficial/network/chain/x/shielded/verify"
)

// The tests run this test binary as the "verifier": with fakeModeFileEnv set, TestMain speaks the
// protocol instead of running tests. The mode file is read on every request, so a test switches
// behaviour without restarting the process.
const fakeModeFileEnv = "ORAMA_FAKE_VERIFIER_MODE_FILE"

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

func TestMain(m *testing.M) {
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
			select {}
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
	f.v = mustNew(t, Config{Path: exe, ChainID: "test-chain", RequestTimeout: timeout, StartTimeout: 10 * time.Second})
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
	if err := f.v.Verify(fakeBundle(), nil); !errors.Is(err, verify.ErrVerifierFault) {
		t.Fatalf("panic code: %v", err)
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
