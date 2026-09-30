package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/DeBrosOfficial/network/e2e/harness/config"
	"github.com/DeBrosOfficial/network/e2e/harness/secrets"
)

// EnvSandbox, set to 1, runs every feature package under bubblewrap with
// the owner's real home hidden behind an empty tmpfs (so the real
// ~/.rootwallet, ~/.ssh and ~/.orama are out of reach) and only the paths
// the run needs bound back. Linux only. It is best-effort: same-uid code
// can still reach other processes (ptrace, /proc/<pid>/mem where the kernel
// allows it) and the ssh-agent socket if one is exported.
const EnvSandbox = "E2E_SANDBOX"

// sandboxTool is the wrapper E2E_SANDBOX uses.
const sandboxTool = "bwrap"

// sandboxPrefix is the stage runner's Prefix: nil unless E2E_SANDBOX=1;
// an error when it is asked for where it cannot work.
func sandboxPrefix(lookup func(string) (string, bool), goos, realHome string, keep []string,
	lookPath func(string) (string, error)) ([]string, error) {
	on, err := config.Bool(lookup, EnvSandbox, false)
	if err != nil || !on {
		return nil, err
	}
	if goos != "linux" {
		return nil, fmt.Errorf("%s=1 needs Linux (bubblewrap); this runner is %s", EnvSandbox, goos)
	}
	bin, err := lookPath(sandboxTool)
	if err != nil {
		return nil, fmt.Errorf("%s=1 needs %s on PATH: %w", EnvSandbox, sandboxTool, err)
	}
	if !filepath.IsAbs(realHome) || realHome == "/" {
		return nil, errors.New("cannot sandbox: the real home directory is unknown")
	}
	args := []string{bin, "--die-with-parent", "--dev-bind", "/", "/", "--tmpfs", realHome}
	for _, p := range keep {
		// Only what lies under the hidden home needs binding back.
		if p != "" && strings.HasPrefix(filepath.Clean(p)+"/", filepath.Clean(realHome)+"/") {
			args = append(args, "--bind", p, p)
		}
	}
	return append(args, "--"), nil
}

// sandboxOf is sandboxPrefix for this runner, keeping the repository, the
// work dir and the Go caches of env bound.
func sandboxOf(lay layout, workDir string, env []string) ([]string, error) {
	realHome, err := secrets.RealHome()
	if err != nil {
		return nil, err
	}
	keep := []string{lay.repo, workDir}
	for _, kv := range env {
		for _, name := range goCacheVars {
			if v, ok := strings.CutPrefix(kv, name+"="); ok {
				keep = append(keep, v)
			}
		}
	}
	return sandboxPrefix(os.LookupEnv, runtime.GOOS, realHome, keep, exec.LookPath)
}
