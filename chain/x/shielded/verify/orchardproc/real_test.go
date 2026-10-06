//go:build cgo && orchardffi

package orchardproc

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/chain/x/shielded/bundle"
	"github.com/DeBrosOfficial/network/chain/x/shielded/verify"
	"github.com/DeBrosOfficial/network/chain/x/shielded/verify/orchard"
)

// These tests start the real out-of-process binary (make orchard-verifier) and compare it with the
// cgo verifier on real Ironwood bundles: the two must give the same verdict on every input.

var vectors = []string{
	"ironwood-1-action", "ironwood-2-action", "ironwood-transfer", "ironwood-unshield", "ironwood-unshield-bond",
}

func binaryPath(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "orchardverifier-bin", "target", "release", "orama-orchard-verifier"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("build the out-of-process verifier first (make orchard-verifier): %v", err)
	}
	return path
}

func load(t *testing.T, name string) (raw []byte, chainID string) {
	t.Helper()
	dir := filepath.Join("..", "..", "orchardffi", "testdata")
	raw, err := os.ReadFile(filepath.Join(dir, name+".bundle"))
	if err != nil {
		t.Fatal(err)
	}
	id, err := os.ReadFile(filepath.Join(dir, "chain-id"))
	if err != nil {
		t.Fatal(err)
	}
	return raw, string(id)
}

func realVerifier(t *testing.T, timeout time.Duration) *Verifier {
	t.Helper()
	_, chainID := load(t, "ironwood-1-action")
	v := mustNew(t, Config{Path: binaryPath(t), ChainID: chainID, RequestTimeout: timeout})
	t.Cleanup(func() { _ = v.Close() })
	return v
}

func TestReal_acceptsTheUnboundVectors(t *testing.T) {
	v := realVerifier(t, 30*time.Second)
	for _, name := range vectors[:3] {
		raw, _ := load(t, name)
		if err := v.Verify(raw, nil); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestReal_unshieldVectorsFailWithoutTheirBindingInBothVerifiers(t *testing.T) {
	v := realVerifier(t, 30*time.Second)
	_, chainID := load(t, "ironwood-1-action")
	for _, name := range vectors[3:] {
		raw, _ := load(t, name)
		if err := v.Verify(raw, nil); !errors.Is(err, verify.ErrSignatureRejected) {
			t.Errorf("%s out of process: %v", name, err)
		}
		if err := mustLibrary(t, chainID).Verify(raw, nil); !errors.Is(err, verify.ErrSignatureRejected) {
			t.Errorf("%s in process: %v", name, err)
		}
	}
}

// The two verifiers agree on every mutation of every vector: the same accept or reject, and the
// same reason.
func TestReal_bothVerifiersAgreeOnEveryMutation(t *testing.T) {
	v := realVerifier(t, 30*time.Second)
	_, chainID := load(t, "ironwood-1-action")
	inProcess := mustLibrary(t, chainID)
	for _, name := range vectors[:3] {
		raw, _ := load(t, name)
		n := int(raw[0])
		proofAt := 1 + n*bundle.ActionLen + bundle.HeaderLen + 3
		sigsAt := proofAt + bundle.ProofLen(n)
		offsets := map[string]int{
			"cv":             1 + 3,
			"nullifier":      1 + 32 + 3,
			"rk":             1 + 64 + 3,
			"cmx":            1 + 96 + 3,
			"epk":            1 + 128 + 3,
			"ciphertext":     1 + 160 + 100,
			"flags":          1 + n*bundle.ActionLen,
			"value balance":  1 + n*bundle.ActionLen + 3,
			"anchor":         1 + n*bundle.ActionLen + 9 + 4,
			"proof":          proofAt + 500,
			"spend auth sig": sigsAt + 5,
			"binding sig":    len(raw) - 10,
		}
		check := func(label string, in []byte) {
			a := v.Verify(in, nil)
			b := inProcess.Verify(in, nil)
			if (a == nil) != (b == nil) || (a != nil && !sameKind(a, b)) {
				t.Errorf("%s/%s: out of process %v, in process %v", name, label, a, b)
			}
		}
		check("untouched", raw)
		for label, at := range offsets {
			mut := bytes.Clone(raw)
			mut[at] ^= 1
			check(label, mut)
		}
		check("truncated", raw[:len(raw)-1])
		check("trailing byte", append(bytes.Clone(raw), 0))
	}
}

func sameKind(a, b error) bool {
	for _, target := range []error{
		verify.ErrMalformed, verify.ErrProofLength, verify.ErrProofRejected, verify.ErrSignatureRejected, verify.ErrVerifierFault,
	} {
		if errors.Is(a, target) != errors.Is(b, target) {
			return false
		}
	}
	return true
}

func TestReal_aWrongChainIDIsRejected(t *testing.T) {
	raw, chainID := load(t, "ironwood-1-action")
	v := mustNew(t, Config{Path: binaryPath(t), ChainID: chainID + "-other"})
	defer v.Close()
	if err := v.Verify(raw, nil); !errors.Is(err, verify.ErrSignatureRejected) {
		t.Fatalf("got %v", err)
	}
}

// A verifier that is stopped answers nothing: the request times out, is rejected, and the next
// request runs on a fresh process.
func TestReal_aStoppedVerifierRejectsThenRecovers(t *testing.T) {
	v := realVerifier(t, 500*time.Millisecond)
	raw, _ := load(t, "ironwood-1-action")
	if err := v.Verify(raw, nil); err != nil {
		t.Fatal(err)
	}
	stopped := v.proc.cmd.Process
	if err := stopped.Signal(syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	err := v.Verify(raw, nil)
	if !errors.Is(err, verify.ErrVerifierFault) || errors.Is(err, verify.ErrTampered) {
		t.Fatalf("stopped verifier: got %v, want a fault that is not a verdict on the bundle", err)
	}
	if err := v.Verify(raw, nil); err != nil {
		t.Fatalf("after the timeout the next request must run on a fresh process: %v", err)
	}
	if v.proc.cmd.Process.Pid == stopped.Pid {
		t.Fatal("the stopped process was reused")
	}
}

// A verifier killed while it works on a request rejects that request and recovers.
func TestReal_aVerifierKilledMidRequestRejectsThenRecovers(t *testing.T) {
	v := realVerifier(t, 30*time.Second)
	raw, _ := load(t, "ironwood-2-action")
	if err := v.Verify(raw, nil); err != nil {
		t.Fatal(err)
	}
	pid := v.proc.cmd.Process
	if err := pid.Signal(syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(200 * time.Millisecond)
		_ = pid.Kill()
	}()
	start := time.Now()
	err := v.Verify(raw, nil)
	if !errors.Is(err, verify.ErrVerifierFault) {
		t.Fatalf("killed mid-request: got %v", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("a killed verifier took %s to fail the request", time.Since(start))
	}
	if err := v.Verify(raw, nil); err != nil {
		t.Fatalf("recover: %v", err)
	}
}

// A verifier that died between requests is started again for the next one and does not fail it.
func TestReal_aVerifierThatDiedIdleIsRestarted(t *testing.T) {
	v := realVerifier(t, 30*time.Second)
	raw, _ := load(t, "ironwood-1-action")
	if err := v.Verify(raw, nil); err != nil {
		t.Fatal(err)
	}
	first := v.proc
	_ = first.cmd.Process.Kill()
	<-first.done
	if err := v.Verify(raw, nil); err != nil {
		t.Fatalf("a valid bundle failed because the process had died while idle: %v", err)
	}
	if v.proc == first {
		t.Fatal("the dead process was reused")
	}
}

func TestReal_manyBundlesInARow(t *testing.T) {
	v := realVerifier(t, 30*time.Second)
	raw, _ := load(t, "ironwood-1-action")
	for i := 0; i < 20; i++ {
		if err := v.Verify(raw, nil); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}
}

func mustLibrary(t *testing.T, chainID string) verify.Verifier {
	t.Helper()
	v, err := orchard.New(chainID)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
