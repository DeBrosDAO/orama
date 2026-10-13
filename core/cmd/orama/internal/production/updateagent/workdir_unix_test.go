//go:build unix

package updateagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckWorkDir_createsAMissingDirectoryOnlyItsOwnerCanWrite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "work")
	if err := checkWorkDir(dir, uint32(os.Getuid())); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm()&writableByOthers != 0 {
		t.Fatalf("created directory: %v, %v", info, err)
	}
}

func TestCheckWorkDir_acceptsAnExistingDirectoryOfTheOwner(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := checkWorkDir(dir, uint32(os.Getuid())); err != nil {
		t.Fatal(err)
	}
}

func TestCheckWorkDir_refusesWhatSomeoneElseCouldChange(t *testing.T) {
	for name, mode := range map[string]os.FileMode{"group-writable": 0o770, "world-writable": 0o707} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Chmod(dir, mode); err != nil {
				t.Fatal(err)
			}
			err := checkWorkDir(dir, uint32(os.Getuid()))
			if err == nil || !strings.Contains(err.Error(), "writable only by its owner") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestCheckWorkDir_refusesADirectoryAnotherUserOwns(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := checkWorkDir(dir, uint32(os.Getuid())+1); err == nil {
		t.Fatal("a directory of another owner was accepted")
	}
}

func TestCheckWorkDir_refusesASymlinkAndAFile(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{"symlink": link, "file": file} {
		if err := checkWorkDir(path, uint32(os.Getuid())); err == nil {
			t.Errorf("a %s was accepted as the work directory", name)
		}
	}
}
