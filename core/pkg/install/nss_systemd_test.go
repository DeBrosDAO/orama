package install

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const debianNsswitch = `# /etc/nsswitch.conf
passwd:         files
group:          files # local first
shadow:         files
hosts:          files dns
`

func newNSS(t *testing.T, conf string, module bool, install func(string) error) nssSystemd {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "nsswitch.conf")
	if err := os.WriteFile(path, []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	if module {
		if err := os.WriteFile(filepath.Join(dir, "libnss_systemd.so.2"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return nssSystemd{nsswitch: path, moduleGlobs: []string{filepath.Join(dir, "libnss_systemd.so.2")}, installPkg: install}
}

func TestEnsureNSS_debianLayoutGetsPackageAndSource(t *testing.T) {
	var installed []string
	var n nssSystemd
	n = newNSS(t, debianNsswitch, false, func(pkg string) error {
		installed = append(installed, pkg)
		return os.WriteFile(n.moduleGlobs[0], nil, 0o644) // the package ships the module
	})
	if err := n.ensure(); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if len(installed) != 1 || installed[0] != "libnss-systemd" {
		t.Errorf("installed %v, want [libnss-systemd]", installed)
	}
	got, _ := os.ReadFile(n.nsswitch)
	for _, want := range []string{"passwd:         files systemd\n", "group:          files systemd # local first\n", "hosts:          files dns\n"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("nsswitch.conf lacks %q:\n%s", want, got)
		}
	}
}

func TestEnsureNSS_isIdempotentAndLeavesAGoodHostAlone(t *testing.T) {
	good := "passwd:         files systemd\ngroup:          files systemd\n"
	n := newNSS(t, good, true, func(string) error { t.Fatal("installed a package that is present"); return nil })
	for i := 0; i < 2; i++ {
		if err := n.ensure(); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	if got, _ := os.ReadFile(n.nsswitch); string(got) != good {
		t.Errorf("a good file was rewritten:\n%s", got)
	}
	// A second pass over an edited Debian file changes nothing more.
	once, changed, err := addNSSSource(debianNsswitch, nssDatabases, nssSource)
	if err != nil || !changed {
		t.Fatalf("first edit: %v changed=%v", err, changed)
	}
	if twice, changed, _ := addNSSSource(once, nssDatabases, nssSource); changed || twice != once {
		t.Errorf("second edit changed the file:\n%s", twice)
	}
}

func TestEnsureNSS_systemdGoesAfterCompatAndSkipsCommentedSources(t *testing.T) {
	got, _, err := addNSSSource("passwd: compat\ngroup: compat\n# passwd: files systemd\n", nssDatabases, nssSource)
	if err != nil || !strings.HasPrefix(got, "passwd:         compat systemd\ngroup:          compat systemd\n") {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestEnsureNSS_failsClosed(t *testing.T) {
	// The package cannot be installed.
	n := newNSS(t, debianNsswitch, false, func(string) error { return errors.New("apt unavailable") })
	if err := n.ensure(); err == nil || !strings.Contains(err.Error(), "apt-get install libnss-systemd") {
		t.Errorf("failed install = %v, want an actionable error", err)
	}
	// An install that leaves no module behind is caught by the verification.
	n = newNSS(t, debianNsswitch, false, func(string) error { return nil })
	if err := n.ensure(); err == nil || !strings.Contains(err.Error(), "libnss_systemd.so.2 is not installed") {
		t.Errorf("no module = %v", err)
	}
	// No group line: the host's lookup order is not invented.
	n = newNSS(t, "passwd: files\n", true, nil)
	if err := n.ensure(); err == nil || !strings.Contains(err.Error(), "no group line") {
		t.Errorf("missing group line = %v", err)
	}
	// Verification alone refuses a file without the source.
	n = newNSS(t, debianNsswitch, true, nil)
	if err := n.verify(); err == nil || !strings.Contains(err.Error(), `no "systemd" source on its passwd line`) {
		t.Errorf("verify = %v", err)
	}
}

func TestEnsureNSS_missingFile(t *testing.T) {
	n := newNSS(t, "", true, nil)
	n.nsswitch = filepath.Join(t.TempDir(), "absent")
	if err := n.ensure(); err == nil {
		t.Error("a missing nsswitch.conf passed")
	}
}

func TestAddNSSSource_bracketStaysBoundToFiles(t *testing.T) {
	cases := map[string]string{
		"passwd: files [NOTFOUND=return] dns\ngroup: files\n":                  "passwd:         files [NOTFOUND=return] systemd dns\n",
		"passwd: files [NOTFOUND=return SUCCESS=continue] dns\ngroup: files\n": "passwd:         files [NOTFOUND=return SUCCESS=continue] systemd dns\n",
		"passwd: files [NOTFOUND=return]\ngroup: files\n":                      "passwd:         files [NOTFOUND=return] systemd\n",
		"passwd: compat [NOTFOUND=return] files\ngroup: files\n":               "passwd:         compat [NOTFOUND=return] systemd files\n",
	}
	for in, want := range cases {
		got, changed, err := addNSSSource(in, nssDatabases, nssSource)
		if err != nil || !changed || !strings.HasPrefix(got, want) {
			t.Errorf("addNSSSource(%q) = %q, %v, %v; want prefix %q", in, got, changed, err, want)
		}
	}
}

func TestAddNSSSource_emptyListIsRefused(t *testing.T) {
	_, _, err := addNSSSource("passwd:\ngroup: files\n", nssDatabases, nssSource)
	if err == nil || !strings.Contains(err.Error(), "line 1") || !strings.Contains(err.Error(), "empty passwd source list") {
		t.Errorf("empty passwd list = %v, want an error naming the line", err)
	}
	_, _, err = addNSSSource("passwd: files\ngroup:   # none\n", nssDatabases, nssSource)
	if err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Errorf("empty group list = %v, want an error naming line 2", err)
	}
}

func TestEditNsswitch_symlinkIsEditedAtItsTarget(t *testing.T) {
	n := newNSS(t, debianNsswitch, true, nil)
	dir := filepath.Dir(n.nsswitch)
	targetDir := filepath.Join(dir, "real")
	if err := os.Mkdir(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(targetDir, "nsswitch.real")
	if err := os.Rename(n.nsswitch, target); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("real", "nsswitch.real"), n.nsswitch); err != nil {
		t.Fatal(err)
	}
	if err := n.ensure(); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if fi, err := os.Lstat(n.nsswitch); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the symlink was replaced by a regular file: %v %v", fi, err)
	}
	got, _ := os.ReadFile(target)
	if !strings.Contains(string(got), "passwd:         files systemd\n") {
		t.Errorf("target not edited:\n%s", got)
	}
	if fi, _ := os.Stat(target); fi.Mode().Perm() != 0o640 {
		t.Errorf("target mode = %v, want 0640", fi.Mode().Perm())
	}
	for _, d := range []string{dir, targetDir} {
		entries, _ := os.ReadDir(d)
		for _, e := range entries {
			if strings.Contains(e.Name(), ".orama-") {
				t.Errorf("temp file %s left in %s", e.Name(), d)
			}
		}
	}
}

func TestReplaceFileAtomic_failureLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "conf")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A directory at path makes the final rename fail after the temp is written.
	bad := filepath.Join(dir, "isdir")
	if err := os.Mkdir(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := replaceFileAtomic(bad, []byte("new")); err == nil {
		t.Fatal("renaming a file over a directory succeeded")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Errorf("directory has %d entries, want conf and isdir only", len(entries))
	}
	if got, _ := os.ReadFile(path); string(got) != "old" {
		t.Errorf("unrelated file changed: %q", got)
	}
}
