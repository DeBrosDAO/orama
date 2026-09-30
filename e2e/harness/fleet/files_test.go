package fleet

import (
	"os"
	"strings"
	"testing"
)

func TestWriteFile_restoresPreviousAndVerifies(t *testing.T) {
	sh := &fakeShell{files: map[string][]byte{"/etc/x.conf": []byte("old")}, modes: map[string]os.FileMode{"/etc/x.conf": 0o640}}
	f := newFake(t, sh)
	var inner *testing.T
	t.Run("write", func(t *testing.T) {
		inner = t
		f.WriteFile(t, f.Node(t, "node-1"), "/etc/x.conf", []byte("new"), 0o600)
		if string(sh.files["/etc/x.conf"]) != "new" {
			t.Fatal("not written")
		}
	})
	if inner.Failed() {
		t.Fatal("the restore was reported as failed")
	}
	if string(sh.files["/etc/x.conf"]) != "old" || sh.modes["/etc/x.conf"] != 0o640 {
		t.Fatalf("cleanup did not restore: %q %o", sh.files["/etc/x.conf"], sh.modes["/etc/x.conf"])
	}
	if last := sh.cmds[len(sh.cmds)-1]; last != "stat -c %a /etc/x.conf" {
		t.Fatalf("the restore was not read back, last command %q", last)
	}
}

func TestWriteFile_removesNewFile(t *testing.T) {
	sh := &fakeShell{}
	f := newFake(t, sh)
	t.Run("write", func(t *testing.T) {
		f.WriteFile(t, f.Node(t, "node-1"), "/tmp/new.txt", []byte("x"), 0o644)
	})
	if _, ok := sh.files["/tmp/new.txt"]; ok {
		t.Fatal("the new file survived the cleanup")
	}
	if last := sh.cmds[len(sh.cmds)-1]; last != "test -e /tmp/new.txt" {
		t.Fatalf("the removal was not verified, last command %q", last)
	}
}

// TestWriteFile_probeErrorIsFatal: an ssh failure (exit 255) of the existence
// probe must not be read as "absent", or the cleanup would delete a file the
// test never created.
func TestWriteFile_probeErrorIsFatal(t *testing.T) {
	sh := &fakeShell{files: map[string][]byte{"/etc/keep": []byte("keep")}, probeExit: 255}
	f := newFake(t, sh)
	inner := runFailTB(t, func(tb testing.TB) {
		f.WriteFile(tb, f.Node(t, "node-1"), "/etc/keep", []byte("x"), 0o600)
	})
	if !inner.Failed() {
		t.Fatal("an unknown existence was accepted")
	}
	if string(sh.files["/etc/keep"]) != "keep" {
		t.Fatalf("the file was touched: %q", sh.files["/etc/keep"])
	}
	for _, c := range sh.cmds {
		if strings.HasPrefix(c, "rm ") || strings.HasPrefix(c, "PUT ") {
			t.Fatalf("ran %q after an inconclusive probe", c)
		}
	}
}

func TestVerifyRestored_detectsDrift(t *testing.T) {
	sh := &fakeShell{files: map[string][]byte{"/a": []byte("changed")}, modes: map[string]os.FileMode{"/a": 0o600}}
	f := newFake(t, sh)
	_ = f
	cases := map[string]fileSnapshot{
		"content": {existed: true, data: []byte("orig"), mode: 0o600},
		"mode":    {existed: true, data: []byte("changed"), mode: 0o644},
		"present": {existed: false},
	}
	for name, snap := range cases {
		if err := verifyRestored(t.Context(), sh, "/a", snap); err == nil {
			t.Errorf("%s drift not detected", name)
		}
	}
	if err := verifyRestored(t.Context(), sh, "/a", fileSnapshot{existed: true, data: []byte("changed"), mode: 0o600}); err != nil {
		t.Fatalf("an exact restore was refused: %v", err)
	}
	if err := verifyRestored(t.Context(), sh, "/missing", fileSnapshot{existed: true, data: []byte("x"), mode: 0o600}); err == nil {
		t.Fatal("a missing file passed as restored")
	}
}
