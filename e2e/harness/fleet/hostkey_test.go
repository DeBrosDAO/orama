package fleet

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func newHostKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	k, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func knownHosts(t *testing.T, lines ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestHostKeyFingerprint_pinnedKey(t *testing.T) {
	k1, k2 := newHostKey(t), newHostKey(t)
	st := &State{KnownHostsFile: knownHosts(t, "# pinned by the provisioner",
		knownhosts.Line([]string{"203.0.113.1"}, k1), knownhosts.Line([]string{"203.0.113.2"}, k2))}
	got, err := HostKeyFingerprint(st, Node{Name: "node-2", PublicIP: "203.0.113.2"})
	if err != nil {
		t.Fatal(err)
	}
	if want := ssh.FingerprintSHA256(k2); got != want || !strings.HasPrefix(got, "SHA256:") {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestHostKeyFingerprint_refusals(t *testing.T) {
	k1, k2 := newHostKey(t), newHostKey(t)
	st := &State{KnownHostsFile: knownHosts(t, knownhosts.Line([]string{"203.0.113.1"}, k1), knownhosts.Line([]string{"203.0.113.1"}, k2))}
	if _, err := HostKeyFingerprint(st, Node{Name: "a", PublicIP: "203.0.113.1"}); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("two keys: %v", err)
	}
	if _, err := HostKeyFingerprint(st, Node{Name: "b", PublicIP: "203.0.113.9"}); err == nil {
		t.Fatal("unpinned host accepted")
	}
	if _, err := HostKeyFingerprint(st, Node{Name: "c"}); err == nil {
		t.Fatal("node without an IP accepted")
	}
	if _, err := HostKeyFingerprint(&State{KnownHostsFile: filepath.Join(t.TempDir(), "none")}, Node{Name: "d", PublicIP: "203.0.113.1"}); err == nil {
		t.Fatal("missing file accepted")
	}
	if _, err := HostKeyFingerprint(&State{KnownHostsFile: knownHosts(t, "203.0.113.1 ssh-ed25519 !!!")}, Node{Name: "e", PublicIP: "203.0.113.1"}); err == nil {
		t.Fatal("corrupt file accepted")
	}
}
