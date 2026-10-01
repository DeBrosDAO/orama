package remotessh

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

// fakeSSH puts an `ssh` on PATH that records its arguments and its stdin.
func fakeSSH(t *testing.T) (argsFile, stdinFile string) {
	t.Helper()
	dir := t.TempDir()
	argsFile = filepath.Join(dir, "args")
	stdinFile = filepath.Join(dir, "stdin")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + "\ncat > " + stdinFile + "\n"
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return argsFile, stdinFile
}

// A secret handed over WithStdin reaches the remote command's stdin and never
// its argv.
func TestRunSSHStreaming_withStdinFeedsTheCommand(t *testing.T) {
	argsFile, stdinFile := fakeSSH(t)
	node := inspector.Node{Host: "1.2.3.4", User: "root", SSHKey: "/dev/null"}

	const secret = `{"token":"s3cret"}`
	if err := RunSSHStreaming(node, "orama node install --secrets-stdin", WithStdin(strings.NewReader(secret))); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(stdinFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != secret {
		t.Errorf("remote stdin = %q, want %q", got, secret)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(args), "s3cret") {
		t.Errorf("the secret is in ssh's argv:\n%s", args)
	}
}

// TestBaseSSHOptions_aDeadSessionFails: a push hung for an hour on a session
// that had died without either end noticing; every scp and ssh call now sends
// keepalives and gives up when a minute of them goes unanswered.
func TestBaseSSHOptions_aDeadSessionFails(t *testing.T) {
	opts := strings.Join(baseSSHOptions(), " ")
	for _, want := range []string{"ConnectTimeout=", "ServerAliveInterval=", "ServerAliveCountMax=", "IdentitiesOnly=yes"} {
		if !strings.Contains(opts, want) {
			t.Errorf("options %q lack %s", opts, want)
		}
	}
	if sshServerAliveInterval*sshServerAliveCountMax > 120 {
		t.Errorf("a dead session is detected after %ds; keep it within two minutes", sshServerAliveInterval*sshServerAliveCountMax)
	}
}
