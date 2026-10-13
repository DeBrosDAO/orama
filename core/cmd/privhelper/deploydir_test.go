package main

import (
	"os"
	"os/user"
	"path/filepath"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rootfs"
)

func currentUser(t *testing.T) string {
	t.Helper()
	u, err := user.Current()
	if err != nil {
		t.Skipf("no current user: %v", err)
	}
	return u.Username
}

func TestVerifyDeploymentDir_acceptsTheStagedDirectory(t *testing.T) {
	anchor := t.TempDir()
	dir := filepath.Join(anchor, ".orama", "data", "deployments", "alice-web")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := verifyDeploymentDir(rootfs.At(anchor), dir, currentUser(t)); err != nil {
		t.Fatalf("a real directory owned by the gateway's user was refused: %v", err)
	}
}

// F3: the orama user could replace the directory with a symlink, and PID 1
// would bind wherever it pointed — another namespace's deployment — into this
// tenant's unit.
func TestVerifyDeploymentDir_refusesASymlink(t *testing.T) {
	anchor := t.TempDir()
	victim := filepath.Join(anchor, ".orama", "data", "deployments", "bob-web")
	if err := os.MkdirAll(victim, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(anchor, ".orama", "data", "deployments", "alice-web")
	if err := os.Symlink(victim, dir); err != nil {
		t.Fatal(err)
	}
	if err := verifyDeploymentDir(rootfs.At(anchor), dir, currentUser(t)); err == nil {
		t.Fatal("a symlinked deployment directory would have been bound")
	}
}

// A directory owned by someone else is not one the gateway staged.
func TestVerifyDeploymentDir_refusesAnotherOwner(t *testing.T) {
	anchor := t.TempDir()
	dir := filepath.Join(anchor, ".orama", "data", "deployments", "alice-web")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := user.Lookup("root"); err != nil {
		t.Skip("no root user to compare against")
	}
	if currentUser(t) == "root" {
		t.Skip("running as root: the directory is root's")
	}
	if err := verifyDeploymentDir(rootfs.At(anchor), dir, "root"); err == nil {
		t.Fatal("a directory not owned by the expected user was accepted")
	}
}

func TestVerifyDeploymentDir_refusesAMissingDirectory(t *testing.T) {
	anchor := t.TempDir()
	if err := verifyDeploymentDir(rootfs.At(anchor), filepath.Join(anchor, ".orama", "data", "deployments", "gone"), currentUser(t)); err == nil {
		t.Fatal("a missing directory was accepted")
	}
}
