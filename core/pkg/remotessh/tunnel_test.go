package remotessh

import (
	"slices"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

func TestTunnelArgs_forwardsALoopbackPortUnderTheNodesHostKeyPolicy(t *testing.T) {
	node := inspector.Node{User: "ubuntu", Host: "203.0.113.5", SSHKey: "/tmp/key", KnownHostsFile: "/tmp/known"}
	args := tunnelArgs(node, 40123, "198.18.0.2:31003")
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"-L 127.0.0.1:40123:198.18.0.2:31003", "-N", "-i /tmp/key", "ubuntu@203.0.113.5",
		"UserKnownHostsFile=/tmp/known", "StrictHostKeyChecking=yes", "ExitOnForwardFailure=yes", "IdentitiesOnly=yes",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("args lack %q: %s", want, joined)
		}
	}
	if args[len(args)-1] != "ubuntu@203.0.113.5" {
		t.Errorf("the destination must come last, got %v", args)
	}
	if !slices.Contains(args, "-N") {
		t.Error("a tunnel runs no remote command")
	}
	if !strings.Contains(joined, "ForwardAgent=no") || !strings.Contains(joined, "BatchMode=yes") {
		t.Error("the operator's agent is not forwarded, and a server's prompt is never put on the terminal")
	}
}

func TestStartTunnel_refusesANodeWithoutAKey(t *testing.T) {
	if _, _, err := StartTunnel(t.Context(), inspector.Node{Host: "203.0.113.5", User: "root"}, "198.18.0.2:31003"); err == nil || !strings.Contains(err.Error(), "no SSH key") {
		t.Fatalf("got %v", err)
	}
}

func TestFreePort_isUsableAndDifferentEachTime(t *testing.T) {
	a, err := freePort()
	if err != nil || a < 1024 {
		t.Fatalf("port %d, %v", a, err)
	}
}

func TestCommand_runsTheCommandUnderTheNodesKeyAndHostKeyPolicy(t *testing.T) {
	node := inspector.Node{User: "root", Host: "203.0.113.5", SSHKey: "/tmp/key", KnownHostsFile: "/tmp/known"}
	cmd, err := Command(t.Context(), node, "echo hi")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(cmd.Args, " ")
	for _, want := range []string{"ssh", "-i /tmp/key", "UserKnownHostsFile=/tmp/known", "StrictHostKeyChecking=yes", "ServerAliveInterval", "ForwardAgent=no", "BatchMode=yes", "ClearAllForwardings=yes", "root@203.0.113.5 echo hi"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args lack %q: %s", want, joined)
		}
	}
	if cmd.Stdout != nil || cmd.Stdin != nil {
		t.Error("the caller decides where the streams go")
	}
}

func TestCommand_refusesANodeWithoutAKey(t *testing.T) {
	if _, err := Command(t.Context(), inspector.Node{Host: "203.0.113.5", User: "root"}, "true"); err == nil {
		t.Fatal("no key, no session")
	}
}

func TestBaseSSHOptions_neverPromptAndNeverForwardAnAgent(t *testing.T) {
	joined := strings.Join(baseSSHOptions(), " ")
	for _, want := range []string{"BatchMode=yes", "ForwardAgent=no", "IdentitiesOnly=yes", "PreferredAuthentications=publickey"} {
		if !strings.Contains(joined, want) {
			t.Errorf("scp and ssh sessions lack %s: %s", want, joined)
		}
	}
}
