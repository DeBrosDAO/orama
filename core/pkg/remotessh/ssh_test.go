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
