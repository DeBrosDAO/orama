package sshcmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/inspector"
)

const testHostKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl"

// pinHome points HOME at a directory whose ~/.orama/known_hosts holds lines,
// or holds nothing at all when lines is nil.
func pinHome(t *testing.T, lines []string) string {
	t.Helper()
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen is not installed")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	if lines == nil {
		return home
	}
	dir := filepath.Join(home, ".orama")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "known_hosts"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

// Bug: orama ssh ran with StrictHostKeyChecking=accept-new, which pins
// whatever key a host offers on first contact.
func TestRequirePinnedHostKey_refusesAHostWithNoPin(t *testing.T) {
	for name, lines := range map[string][]string{
		"no known_hosts file":      nil,
		"another host is pinned":   {"10.9.9.9 " + testHostKey},
		"an empty known_hosts":     {""},
		"only a comment is pinned": {"# 1.2.3.4 " + testHostKey},
	} {
		t.Run(name, func(t *testing.T) {
			home := pinHome(t, lines)
			node := inspector.Node{Host: "1.2.3.4", User: "root"}
			err := requirePinnedHostKey(&node)
			if err == nil {
				t.Fatal("a host with no pinned key was accepted")
			}
			if got := clierr.CodeOf(err); got != clierr.CodeFailure {
				t.Errorf("exit code %d, want %d", got, clierr.CodeFailure)
			}
			for _, want := range []string{"Host key verification failed", "1.2.3.4", "orama node setup", filepath.Join(home, ".orama", "known_hosts")} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal lacks %q: %v", want, err)
				}
			}
			if _, statErr := os.Stat(filepath.Join(home, ".ssh", "known_hosts")); statErr == nil {
				t.Error("a refused host was written to ~/.ssh/known_hosts")
			}
		})
	}
}

func TestRequirePinnedHostKey_acceptsAPinnedHost(t *testing.T) {
	home := pinHome(t, []string{"10.9.9.9 " + testHostKey, "1.2.3.4 " + testHostKey})
	node := inspector.Node{Host: "1.2.3.4", User: "root"}
	if err := requirePinnedHostKey(&node); err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".orama", "known_hosts"); node.KnownHostsFile != want {
		t.Errorf("KnownHostsFile = %q, want %q", node.KnownHostsFile, want)
	}
}

// A node that already names its pinned file (as setup's does) keeps it.
func TestRequirePinnedHostKey_keepsTheNodesOwnPin(t *testing.T) {
	pinHome(t, nil)
	own := filepath.Join(t.TempDir(), "run_known_hosts")
	if err := os.WriteFile(own, []byte("1.2.3.4 "+testHostKey+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	node := inspector.Node{Host: "1.2.3.4", User: "root", KnownHostsFile: own}
	if err := requirePinnedHostKey(&node); err != nil {
		t.Fatal(err)
	}
	if node.KnownHostsFile != own {
		t.Errorf("KnownHostsFile replaced: %q", node.KnownHostsFile)
	}
}

func TestSSHArgs_checksTheHostKeyStrictlyAgainstThePin(t *testing.T) {
	node := inspector.Node{Host: "1.2.3.4", User: "root", SSHKey: "/tmp/key", KnownHostsFile: "/h/.orama/known_hosts"}
	args := sshArgs(node, "uptime")
	for _, want := range []string{"StrictHostKeyChecking=yes", "UserKnownHostsFile=/h/.orama/known_hosts", "IdentitiesOnly=yes", "root@1.2.3.4"} {
		if !slices.Contains(args, want) {
			t.Errorf("args %q lack %q", args, want)
		}
	}
	for _, a := range args {
		if strings.Contains(a, "accept-new") || strings.Contains(a, "StrictHostKeyChecking=no") {
			t.Errorf("args %q trust a host on first use", args)
		}
	}
	if args[len(args)-1] != "uptime" {
		t.Errorf("the remote command is not last: %q", args)
	}
	if bare := sshArgs(node, ""); bare[len(bare)-1] != "root@1.2.3.4" {
		t.Errorf("an interactive session carries a command: %q", bare)
	}
}
