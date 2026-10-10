package secrets

import (
	"errors"
	"fmt"
	"os/user"
	"path/filepath"
	"strings"
)

// RootWalletDirName is the directory under the owner's real home that holds the
// real RootWallet and its agent socket. No e2e run may ever talk to it.
const RootWalletDirName = ".rootwallet"

// ErrAgentSockEmpty is returned when RW_AGENT_SOCK is not set. The orama CLI
// would then fall back to ~/.rootwallet/agent.sock, the owner's real wallet.
var ErrAgentSockEmpty = errors.New("RW_AGENT_SOCK is empty: the orama CLI would fall back to the real ~/.rootwallet agent")

// RealHome is the home directory of the user running the harness, read from the
// user database rather than $HOME: the harness sets HOME to an isolated
// directory for the CLI, so $HOME says nothing about where the real wallet is.
func RealHome() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("failed to look up the current user: %w", err)
	}
	if u.HomeDir == "" {
		return "", fmt.Errorf("the user database has no home directory for %s", u.Username)
	}
	return u.HomeDir, nil
}

// within reports whether path is dir or inside it, on cleaned absolute paths.
func within(path, dir string) bool {
	path, dir = filepath.Clean(path), filepath.Clean(dir)
	if path == dir {
		return true
	}
	return strings.HasPrefix(path, dir+string(filepath.Separator))
}

// CheckAgentSockNotRealWallet refuses an empty or relative socket path, and one
// inside realHome/.rootwallet. It is the preflight check.
func CheckAgentSockNotRealWallet(sock, realHome string) error {
	if err := checkSockShape(sock); err != nil {
		return err
	}
	if realHome == "" {
		return errors.New("cannot check RW_AGENT_SOCK: the real home directory is unknown")
	}
	wallet := filepath.Join(realHome, RootWalletDirName)
	inside, err := insideEither(sock, wallet)
	if err != nil {
		return fmt.Errorf("cannot check RW_AGENT_SOCK=%s against the real %s: %w", sock, wallet, err)
	}
	if inside {
		return fmt.Errorf("RW_AGENT_SOCK=%s is inside the real %s: an e2e run must use its throwaway agent", sock, wallet)
	}
	return nil
}

// CheckAgentSockOutsideHome refuses an empty or relative socket path, and one
// anywhere inside realHome. It is the check the CLI runner applies before every
// invocation: the throwaway agent lives in the run's short temporary directory.
func CheckAgentSockOutsideHome(sock, realHome string) error {
	if err := checkSockShape(sock); err != nil {
		return err
	}
	if realHome == "" {
		return errors.New("cannot check RW_AGENT_SOCK: the real home directory is unknown")
	}
	inside, err := insideEither(sock, realHome)
	if err != nil {
		return fmt.Errorf("cannot check RW_AGENT_SOCK=%s against the real home %s: %w", sock, realHome, err)
	}
	if inside {
		return fmt.Errorf("RW_AGENT_SOCK=%s is inside the real home %s: the CLI must sign through the run's throwaway agent", sock, realHome)
	}
	return nil
}

func checkSockShape(sock string) error {
	if strings.TrimSpace(sock) == "" {
		return ErrAgentSockEmpty
	}
	if !filepath.IsAbs(sock) {
		return fmt.Errorf("RW_AGENT_SOCK=%s is not an absolute path", sock)
	}
	return nil
}
