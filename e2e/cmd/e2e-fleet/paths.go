package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/e2e/harness/config"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/secrets"
)

// modulePath identifies the e2e module's go.mod.
const modulePath = "github.com/DeBrosOfficial/network/e2e"

// Paths inside the e2e module.
const (
	featuresDir = "features"
	waiversFile = "waivers.yaml"
)

// moduleRoot walks up from dir to the directory whose go.mod declares the e2e module.
func moduleRoot(dir string) (string, error) {
	for d := dir; ; d = filepath.Dir(d) {
		if declaresModule(filepath.Join(d, "go.mod")) {
			return d, nil
		}
		if filepath.Dir(d) == d {
			return "", fmt.Errorf("no go.mod declaring %s above %s: run e2e-fleet from the e2e module", modulePath, dir)
		}
	}
}

func declaresModule(goMod string) bool {
	f, err := os.Open(goMod)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if fields := strings.Fields(sc.Text()); len(fields) == 2 && fields[0] == "module" {
			return fields[1] == modulePath
		}
	}
	return false
}

// layout is where the module and the repository are.
type layout struct {
	module string // e2e/
	repo   string // the repository root, e2e's parent
}

func findLayout() (layout, error) {
	wd, err := os.Getwd()
	if err != nil {
		return layout{}, fmt.Errorf("failed to read the working directory: %w", err)
	}
	mod, err := moduleRoot(wd)
	if err != nil {
		return layout{}, err
	}
	return layout{module: mod, repo: filepath.Dir(mod)}, nil
}

// commitOf is HEAD of the repository, suffixed -dirty when the tree has
// changes: a report must name exactly what it tested.
func commitOf(ctx context.Context, repo string) (string, error) {
	head, err := exec.CommandContext(ctx, "git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("failed to read HEAD of %s: %w", repo, err)
	}
	status, err := exec.CommandContext(ctx, "git", "-C", repo, "status", "--porcelain").Output()
	if err != nil {
		return "", fmt.Errorf("failed to read the working tree state of %s: %w", repo, err)
	}
	commit := strings.TrimSpace(string(head))
	if len(strings.TrimSpace(string(status))) > 0 {
		commit += "-dirty"
	}
	return commit, nil
}

// loadState reads the fleet state from E2E_FLEET_STATE and applies the run
// guards to it: test, report, teardown and the hooks never act on a state
// that points at a shared environment, whoever wrote it.
func loadState() (*fleet.State, string, error) {
	realHome, err := secrets.RealHome()
	if err != nil {
		return nil, "", err
	}
	return loadStateFrom(os.Getenv(config.EnvState), realHome)
}

func loadStateFrom(path, realHome string) (*fleet.State, string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, "", fmt.Errorf("%s is not set: point it at the state.json `e2e-fleet provision` printed", config.EnvState)
	}
	st, err := fleet.Load(path)
	if err != nil {
		return nil, "", err
	}
	if err := checkProvisioned(st, realHome); err != nil {
		return nil, "", fmt.Errorf("refusing the fleet state %s: %w", path, err)
	}
	return st, path, nil
}

// requireArgs checks positional argument count.
func requireArgs(args []string, n int, form string) error {
	if len(args) != n {
		return errors.Join(errUsage, fmt.Errorf("expected: %s", form))
	}
	return nil
}
