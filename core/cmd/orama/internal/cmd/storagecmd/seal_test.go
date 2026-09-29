package storagecmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSecret(t *testing.T, dir, name, hexValue string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(hexValue+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSealAndOpenRoundTrip(t *testing.T) {
	dir := t.TempDir()
	key := writeSecret(t, dir, "key", strings.Repeat("11", 32))
	repair := writeSecret(t, dir, "repair", strings.Repeat("22", 32))
	wrong := writeSecret(t, dir, "wrong", strings.Repeat("99", 32))
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
		"--storage-key-file", key,
		"--repair-seed-file", repair,
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
		"--storage-key-file", key,
		"--repair-seed-file", repair,
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
		"--storage-key-file", wrong,
		"--repair-seed-file", repair,
		"--nonce", strings.Repeat("33", 32),
		"--slot", "1",
		"--in", filepath.Join(out, "slot-1"),
		"--out", filepath.Join(dir, "nope"),
	})
	if err := Cmd.Execute(); err == nil {
		t.Fatal("wrong storage key opened the file")
	}
}

func TestSecretFile_refusesAReadableOrShortSeed(t *testing.T) {
	dir := t.TempDir()
	open := writeSecret(t, dir, "open", strings.Repeat("11", 32))
	if err := os.Chmod(open, 0o644); err != nil {
		t.Fatal(err)
	}
	short := writeSecret(t, dir, "short", "abcd")
	for _, path := range []string{open, short, filepath.Join(dir, "absent")} {
		Cmd.SetArgs([]string{"rewrap", "--repair-seed-file", path, "--nonce", strings.Repeat("33", 32),
			"--in", path, "--out", filepath.Join(dir, "out")})
		if err := Cmd.Execute(); err == nil {
			t.Fatalf("%s was accepted", path)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "out")); err == nil {
		t.Fatal("a refused seed wrote output")
	}
}

func TestStorageKeyFile_refusesAnythingButA32ByteKey(t *testing.T) {
	dir := t.TempDir()
	repair := writeSecret(t, dir, "repair", strings.Repeat("22", 32))
	in := filepath.Join(dir, "plain")
	if err := os.WriteFile(in, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	loose := writeSecret(t, dir, "loose", strings.Repeat("11", 32))
	if err := os.Chmod(loose, 0o644); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"31 bytes":       writeSecret(t, dir, "k31", strings.Repeat("11", 31)),
		"33 bytes":       writeSecret(t, dir, "k33", strings.Repeat("11", 33)),
		"wallet seed":    writeSecret(t, dir, "k64", strings.Repeat("11", 64)),
		"not hex":        writeSecret(t, dir, "bad", "zz"),
		"world-readable": loose,
		"absent":         filepath.Join(dir, "absent"),
	}
	for name, path := range cases {
		out := filepath.Join(dir, "slots-"+strings.ReplaceAll(name, " ", "-"))
		Cmd.SetArgs([]string{"seal", "--storage-key-file", path, "--repair-seed-file", repair,
			"--nonce", strings.Repeat("33", 32), "--in", in, "--out-dir", out})
		if err := Cmd.Execute(); err == nil {
			t.Fatalf("%s was accepted", name)
		}
		if _, err := os.Stat(out); err == nil {
			t.Fatalf("%s wrote output", name)
		}
	}
}

func TestSeal_seedFileFlagIsGone(t *testing.T) {
	for _, name := range []string{"seal", "open", "get"} {
		c, _, err := Cmd.Find([]string{name})
		if err != nil {
			t.Fatal(err)
		}
		if c.Flags().Lookup("seed-file") != nil {
			t.Fatalf("%s still takes --seed-file", name)
		}
		if c.Flags().Lookup("storage-key-file") == nil {
			t.Fatalf("%s lacks --storage-key-file", name)
		}
	}
}
