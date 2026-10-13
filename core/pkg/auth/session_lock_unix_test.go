//go:build unix

package auth

import (
	"os"
	"testing"
	"time"
)

// The lock is what keeps two processes from renewing one session at once. A
// second descriptor is a second holder to flock, as another process would be.
func TestLockCredentialFile_waitsForTheHolder(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	unlock, err := lockCredentialFile()
	if err != nil {
		t.Fatalf("lockCredentialFile: %v", err)
	}

	acquired := make(chan func())
	go func() {
		second, err := lockCredentialFile()
		if err != nil {
			t.Errorf("second lockCredentialFile: %v", err)
			close(acquired)
			return
		}
		acquired <- second
	}()

	select {
	case <-acquired:
		t.Fatal("a second holder took the lock while the first held it")
	case <-time.After(100 * time.Millisecond):
	}

	unlock()
	select {
	case second := <-acquired:
		if second != nil {
			second()
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the lock was not handed over after it was released")
	}
}

func TestLockCredentialFile_isPrivate(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	unlock, err := lockCredentialFile()
	if err != nil {
		t.Fatalf("lockCredentialFile: %v", err)
	}
	defer unlock()

	credPath, err := GetCredentialsPath()
	if err != nil {
		t.Fatalf("GetCredentialsPath: %v", err)
	}
	info, err := os.Stat(credPath + credentialLockSuffix)
	if err != nil {
		t.Fatalf("stat the lock: %v", err)
	}
	if perm := info.Mode().Perm(); perm != credentialLockPerm {
		t.Errorf("lock file mode = %v, want %v", perm, os.FileMode(credentialLockPerm))
	}
}
