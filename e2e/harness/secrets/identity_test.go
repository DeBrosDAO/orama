package secrets

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCheckAgentSock_linkIntoTheRealWalletRefused: a socket reached through
// a symlink into the real wallet, or into the real home, is refused though
// its path names neither.
func TestCheckAgentSock_linkIntoTheRealWalletRefused(t *testing.T) {
	home := t.TempDir()
	wallet := filepath.Join(home, RootWalletDirName)
	if err := os.Mkdir(wallet, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "isolated-looking")
	if err := os.Symlink(wallet, link); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(link, "agent.sock")
	if err := CheckAgentSockNotRealWallet(sock, home); err == nil {
		t.Fatal("a socket through a link into the real wallet passed the preflight")
	}
	if err := CheckAgentSockOutsideHome(sock, home); err == nil {
		t.Fatal("a socket through a link into the real home passed the CLI check")
	}
	if err := CheckAgentSockNotRealWallet(filepath.Join(t.TempDir(), "a.sock"), home); err != nil {
		t.Fatalf("an isolated socket: %v", err)
	}
}

func TestInsideByIdentity_missingDirHoldsNothing(t *testing.T) {
	if in, err := InsideByIdentity("/tmp/x.sock", filepath.Join(t.TempDir(), "absent")); in || err != nil {
		t.Fatalf("in %v err %v", in, err)
	}
}

func TestMakePrivateDir_tightensOwnRefusesLinks(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := MakePrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(dir); info.Mode().Perm() != 0o700 {
		t.Fatalf("mode %v", info.Mode())
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if err := MakePrivateDir(link); err == nil {
		t.Fatal("a link was accepted as the work dir")
	}
	if err := CheckOwnedDir("/", 0o700); err == nil {
		t.Fatal("a directory of another user (root) with another mode was accepted")
	}
}
