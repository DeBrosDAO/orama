package installers

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeNtfySystem records the commands the ntfy installer runs and answers `id ntfy`
// as a machine with or without the ntfy account would.
type fakeNtfySystem struct {
	hasAccount bool
	useraddErr error
	commands   []string
}

func (f *fakeNtfySystem) run(name string, args ...string) (string, error) {
	f.commands = append(f.commands, strings.Join(append([]string{name}, args...), " "))
	switch name {
	case "id":
		if !f.hasAccount {
			return "id: 'ntfy': no such user", errors.New("exit status 1")
		}
	case "useradd":
		if f.useraddErr != nil {
			return "useradd: cannot lock /etc/passwd", f.useraddErr
		}
		f.hasAccount = true
	case "chown":
		if !f.hasAccount {
			return "chown: invalid user: 'ntfy:ntfy'", errors.New("exit status 1")
		}
	}
	return "", nil
}

func (f *fakeNtfySystem) ran(prefix string) bool {
	for _, c := range f.commands {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func newFakeNtfyInstaller(t *testing.T, sys *fakeNtfySystem, binaryInPlace bool) *NtfyInstaller {
	t.Helper()
	return &NtfyInstaller{
		BaseInstaller: NewBaseInstaller("amd64", io.Discard),
		run:           sys.run,
		installed:     func() bool { return binaryInPlace },
		root:          t.TempDir(),
	}
}

// The live stagenet create run of 2026-10-10: a nuclear wipe had deleted the ntfy
// account but left /usr/local/bin/ntfy, Install returned "already installed", and
// Configure's chown failed with "invalid user: 'ntfy:ntfy'".
func TestNtfyInstall_binaryInPlaceButAccountGoneRecreatesTheAccount(t *testing.T) {
	sys := &fakeNtfySystem{}
	ni := newFakeNtfyInstaller(t, sys, true)
	if err := ni.Install(); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !sys.ran("useradd --system --no-create-home --shell /usr/sbin/nologin ntfy") {
		t.Fatalf("Install did not create the missing ntfy account; ran %q", sys.commands)
	}
	if err := ni.Configure("https://push.example.org"); err != nil {
		t.Fatalf("Configure after Install: %v", err)
	}
}

func TestNtfyInstall_accountPresentIsNotRecreated(t *testing.T) {
	sys := &fakeNtfySystem{hasAccount: true}
	ni := newFakeNtfyInstaller(t, sys, true)
	if err := ni.Install(); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if sys.ran("useradd") {
		t.Fatalf("Install ran useradd for an existing account; ran %q", sys.commands)
	}
	if _, err := os.Stat(filepath.Join(ni.root, ntfyDataDir)); err != nil {
		t.Fatalf("data directory not laid out: %v", err)
	}
}

func TestNtfyConfigure_withoutInstallStillEnsuresTheAccount(t *testing.T) {
	sys := &fakeNtfySystem{}
	ni := newFakeNtfyInstaller(t, sys, true)
	if err := ni.Configure("https://push.example.org"); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	cfg, err := os.ReadFile(filepath.Join(ni.root, ntfyConfigPath))
	if err != nil {
		t.Fatalf("server.yml not written: %v", err)
	}
	if !strings.Contains(string(cfg), "https://push.example.org") {
		t.Fatalf("server.yml lacks the base URL:\n%s", cfg)
	}
}

func TestNtfyInstall_useraddFailureStopsBeforeAnyDirectory(t *testing.T) {
	sys := &fakeNtfySystem{useraddErr: errors.New("exit status 1")}
	ni := newFakeNtfyInstaller(t, sys, true)
	err := ni.Install()
	if err == nil || !strings.Contains(err.Error(), "create user") || !strings.Contains(err.Error(), "cannot lock /etc/passwd") {
		t.Fatalf("Install error = %v, want the useradd failure with its output", err)
	}
	if _, statErr := os.Stat(filepath.Join(ni.root, ntfyDataDir)); !os.IsNotExist(statErr) {
		t.Fatalf("data directory created although the account could not be: %v", statErr)
	}
}
