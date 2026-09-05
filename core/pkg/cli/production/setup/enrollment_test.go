package setup

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Enrollment sends the VPS password over a connection whose host key the
// operator has confirmed, and installs the key without repeating it. These
// tests lock both properties in.

func TestInstallKeyArgs_NeverCarriesThePasswordAndPinsTheHostKey(t *testing.T) {
	const password = "sup3r-secret-vps-pass"
	args := installKeyArgs("1.2.3.4", "root", "/tmp/kh/known_hosts")

	joined := strings.Join(args, " ")
	if strings.Contains(joined, password) {
		t.Fatal("the password must never appear in argv: ps exposes it to every local process")
	}
	for _, forbidden := range []string{"-p", "StrictHostKeyChecking=no"} {
		for _, a := range args {
			if a == forbidden {
				t.Errorf("argument %q must not be used: %s", forbidden,
					map[string]string{
						"-p":                       "puts the password on the command line",
						"StrictHostKeyChecking=no": "accepts any host key on the connection that carries the password",
					}[forbidden])
			}
		}
	}

	mustContain(t, args, "-e")                                     // password read from SSHPASS
	mustContain(t, args, "StrictHostKeyChecking=yes")              // host key enforced
	mustContain(t, args, "UserKnownHostsFile=/tmp/kh/known_hosts") // against the pinned file
}

func mustContain(t *testing.T, args []string, want string) {
	t.Helper()
	for _, a := range args {
		if a == want {
			return
		}
	}
	t.Errorf("expected argument %q in %v", want, args)
}

// Re-running setup on a node that is already enrolled must not append the key
// again. The script is executed for real against a throwaway HOME so this
// tests the shell, not a description of it.
func TestInstallKeyScript_IsIdempotent(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	home := t.TempDir()
	const pubKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyMaterial orama@node"

	run := func() string {
		cmd := exec.Command(bash, "-c", installKeyScript)
		cmd.Env = append(os.Environ(), "HOME="+home)
		cmd.Stdin = strings.NewReader(pubKey + "\n")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("install script failed: %v (%s)", err, out)
		}
		return string(out)
	}

	for i := 0; i < 3; i++ {
		if out := run(); !strings.Contains(out, "key installed") {
			t.Fatalf("run %d did not confirm installation: %s", i+1, out)
		}
	}

	data, err := os.ReadFile(filepath.Join(home, ".ssh", "authorized_keys"))
	if err != nil {
		t.Fatalf("read authorized_keys: %v", err)
	}
	if got := strings.Count(string(data), pubKey); got != 1 {
		t.Fatalf("key present %d times after 3 runs, want exactly 1:\n%s", got, data)
	}
}

// A key whose comment contains shell metacharacters must be stored verbatim,
// not executed. The key travels on stdin precisely so this cannot happen.
func TestInstallKeyScript_DoesNotInterpretTheKey(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	home := t.TempDir()
	canary := filepath.Join(home, "pwned")
	hostile := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample '; touch " + canary + " ; echo '"

	cmd := exec.Command(bash, "-c", installKeyScript)
	cmd.Env = append(os.Environ(), "HOME="+home)
	cmd.Stdin = strings.NewReader(hostile + "\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("install script failed: %v (%s)", err, out)
	}

	if _, err := os.Stat(canary); err == nil {
		t.Fatal("the key was interpreted by the shell instead of being stored")
	}
	data, _ := os.ReadFile(filepath.Join(home, ".ssh", "authorized_keys"))
	if !strings.Contains(string(data), hostile) {
		t.Fatalf("key was not stored verbatim:\n%s", data)
	}
}

func TestHostKeyMatches_AcceptsEitherFingerprintForm(t *testing.T) {
	hk := &hostKey{fingerprints: []string{"SHA256:abc123", "SHA256:def456"}}

	for _, want := range []string{"SHA256:abc123", "abc123", "  SHA256:def456  "} {
		if !hk.matches(want) {
			t.Errorf("expected %q to match", want)
		}
	}
	for _, want := range []string{"", "SHA256:nope", "abc"} {
		if hk.matches(want) {
			t.Errorf("expected %q not to match", want)
		}
	}
}

func TestConfirmHostKey_RefusesAMismatchedPin(t *testing.T) {
	hk := &hostKey{fingerprints: []string{"SHA256:actual"}}
	var out bytes.Buffer

	err := confirmHostKey(hk, "1.2.3.4", "SHA256:expected", strings.NewReader(""), &out)
	if err == nil {
		t.Fatal("a fingerprint mismatch must stop enrollment before the password is sent")
	}
	if !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("error should name the mismatch, got: %v", err)
	}
}

func TestConfirmHostKey_AcceptsAMatchingPinWithoutPrompting(t *testing.T) {
	hk := &hostKey{fingerprints: []string{"SHA256:actual"}}
	var out bytes.Buffer

	// Empty stdin: a prompt here would fail, proving none was shown.
	if err := confirmHostKey(hk, "1.2.3.4", "SHA256:actual", strings.NewReader(""), &out); err != nil {
		t.Fatalf("matching pin should be accepted, got: %v", err)
	}
}

func TestConfirmHostKey_InteractiveAnswerDecides(t *testing.T) {
	hk := &hostKey{fingerprints: []string{"SHA256:actual"}}

	for _, tc := range []struct {
		answer string
		wantOK bool
	}{
		{"y\n", true},
		{"yes\n", true},
		{"n\n", false},
		{"\n", false},
		{"", false}, // no input at all must not be read as consent
	} {
		var out bytes.Buffer
		err := confirmHostKey(hk, "1.2.3.4", "", strings.NewReader(tc.answer), &out)
		if tc.wantOK && err != nil {
			t.Errorf("answer %q should continue, got: %v", tc.answer, err)
		}
		if !tc.wantOK && err == nil {
			t.Errorf("answer %q must not be read as confirmation", tc.answer)
		}
		if !strings.Contains(out.String(), "SHA256:actual") {
			t.Errorf("the fingerprint must be shown to the operator, got: %s", out.String())
		}
	}
}
