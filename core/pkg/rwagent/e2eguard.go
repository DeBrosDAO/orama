package rwagent

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
)

// E2EEnvVar marks a process as part of an e2e fleet run (ORAMA_E2E=1). Such a
// process signs only through the run's throwaway agent, named explicitly by
// RW_AGENT_SOCK, and never through the operator's own wallet.
const E2EEnvVar = "ORAMA_E2E"

// walletDirName is where RootWallet keeps the wallet and its agent socket,
// under the account's home.
const walletDirName = ".rootwallet"

// ErrE2EGuard is wrapped by every refusal of the e2e guard, so a caller can
// tell "this run may not use that agent" from "no agent answers".
var ErrE2EGuard = errors.New(E2EEnvVar + "=1")

// ErrE2EDefaultSocket is returned under ORAMA_E2E=1 when no socket was named:
// the default would be the operator's ~/.rootwallet/agent.sock.
var ErrE2EDefaultSocket = fmt.Errorf("%w: RW_AGENT_SOCK is empty: refusing the default "+
	"~/.rootwallet/agent.sock, which is the operator's real wallet; set RW_AGENT_SOCK to the e2e run's test agent", ErrE2EGuard)

// realHomeDir is the account's home from the user database, looked up by
// uid. $HOME is overridden during an e2e run, so it cannot say where the real
// wallet is, and user.Current is not used because without cgo it falls back
// to $HOME when the database has no entry. A variable so tests can point it
// elsewhere.
var realHomeDir = func() (string, error) {
	uid := strconv.Itoa(os.Getuid())
	u, err := user.LookupId(uid)
	if err != nil {
		return "", fmt.Errorf("failed to look up uid %s in the user database: %w", uid, err)
	}
	if u.HomeDir == "" {
		return "", fmt.Errorf("the user database names no home directory for uid %s", uid)
	}
	return u.HomeDir, nil
}

// e2eGuard refuses, under ORAMA_E2E=1, a socket that is empty (the default)
// or that is, or lies inside, the real ~/.rootwallet. Outside an e2e run it
// allows everything. When the real home cannot be found it refuses: the
// guard fails closed.
func e2eGuard(socketPath string) error {
	if os.Getenv(E2EEnvVar) != "1" {
		return nil
	}
	if socketPath == "" {
		return ErrE2EDefaultSocket
	}
	home, err := realHomeDir()
	if err != nil {
		return fmt.Errorf("%w: failed to find the real home directory to keep this run away from its RootWallet: %w", ErrE2EGuard, err)
	}
	return checkE2ESocket(socketPath, home)
}

// checkE2ESocket refuses socketPath when any directory it lies in is the real
// .rootwallet directory, or when it is the real agent socket itself. Both are
// compared by file identity (device and inode), not by name, so a path that
// differs only in case on a case-insensitive file system, a symlink and a
// hard link to the real socket are all refused.
func checkE2ESocket(socketPath, realHome string) error {
	walletDir := filepath.Join(realHome, walletDirName)
	wallet, err := os.Stat(walletDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w: failed to stat the real RootWallet directory %s: %w", ErrE2EGuard, walletDir, err)
	}
	abs, err := filepath.Abs(socketPath)
	if err != nil {
		return fmt.Errorf("%w: failed to make RW_AGENT_SOCK %s absolute: %w", ErrE2EGuard, socketPath, err)
	}
	for dir := filepath.Dir(abs); ; dir = filepath.Dir(dir) {
		if fi, err := os.Stat(dir); err == nil && os.SameFile(fi, wallet) {
			return insideWalletError(socketPath, walletDir)
		}
		if dir == filepath.Dir(dir) {
			break
		}
	}
	return checkNotRealSocket(abs, socketPath, walletDir)
}

// checkNotRealSocket refuses abs when it is the real agent socket by identity.
func checkNotRealSocket(abs, socketPath, walletDir string) error {
	realPath := filepath.Join(walletDir, DefaultSocketName)
	realSock, err := os.Stat(realPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w: failed to stat the real agent socket %s: %w", ErrE2EGuard, realPath, err)
	}
	if fi, err := os.Stat(abs); err == nil && os.SameFile(fi, realSock) {
		return fmt.Errorf("%w: RW_AGENT_SOCK %s is the real RootWallet agent socket in %s (a link to it); "+
			"point it at the e2e run's test agent", ErrE2EGuard, socketPath, walletDir)
	}
	return nil
}

func insideWalletError(socketPath, walletDir string) error {
	return fmt.Errorf("%w: RW_AGENT_SOCK %s resolves inside the real RootWallet directory %s; "+
		"point it at the e2e run's test agent", ErrE2EGuard, socketPath, walletDir)
}
