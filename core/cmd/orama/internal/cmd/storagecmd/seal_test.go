package storagecmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSealAndOpenRoundTrip(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "plain")
	if err := os.WriteFile(in, []byte("private bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "slots")
	buf := &bytes.Buffer{}
	Cmd.SetOut(buf)
	Cmd.SetErr(buf)
	Cmd.SetArgs([]string{
		"seal",
		"--seed", strings.Repeat("11", 32),
		"--repair-seed", strings.Repeat("22", 32),
		"--nonce", strings.Repeat("33", 32),
		"--replicas", "2",
		"--in", in,
		"--out-dir", out,
	})
	if err := Cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	opened := filepath.Join(dir, "opened")
	Cmd.SetArgs([]string{
		"open",
		"--seed", strings.Repeat("11", 32),
		"--repair-seed", strings.Repeat("22", 32),
		"--nonce", strings.Repeat("33", 32),
		"--slot", "1",
		"--in", filepath.Join(out, "slot-1"),
		"--out", opened,
	})
	if err := Cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(opened)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "private bytes" {
		t.Fatalf("opened %q", got)
	}
	Cmd.SetArgs([]string{
		"open",
		"--seed", strings.Repeat("99", 32),
		"--repair-seed", strings.Repeat("22", 32),
		"--nonce", strings.Repeat("33", 32),
		"--slot", "1",
		"--in", filepath.Join(out, "slot-1"),
		"--out", filepath.Join(dir, "nope"),
	})
	if err := Cmd.Execute(); err == nil {
		t.Fatal("wrong seed opened the file")
	}
}
