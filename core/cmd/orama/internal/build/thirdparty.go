package build

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

// The third-party Go programs in the archive (Olric, IPFS Cluster, Caddy) are
// built from modules checked in to this repository, each with its own go.mod
// and go.sum, and `go build -mod=readonly`: the toolchain refuses any module
// whose checksum the go.sum does not list, and a module that changed since it
// was pinned fails its checksum. The versions are in constants/versions.go; a
// test holds each module's go.mod to them.
const (
	// thirdPartyDir holds the Olric and IPFS Cluster modules, beside core's
	// go.mod.
	thirdPartyDir = "thirdparty"
	// caddyMainPackage is the package, in the repository's caddy/ module, that
	// links Caddy with the Orama modules.
	caddyMainPackage = "./cmd/caddy"
)

// buildPinned compiles package pkg of the module in dir into the archive's
// bin/ as name.
func (b *Builder) buildPinned(dir, pkg, name string) error {
	if _, err := os.Stat(filepath.Join(dir, "go.sum")); err != nil {
		return fmt.Errorf("the pinned module for %s is missing its go.sum at %s: %w", name, dir, err)
	}
	cmd := exec.Command("go", goBuildCommandArgs(goLDFlags, filepath.Join(b.binDir, name), pkg)...)
	cmd.Dir = dir
	cmd.Env = b.crossEnv()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("build %s in %s: %w", name, dir, err)
	}
	fmt.Printf("  ✓ %s\n", name)
	return nil
}

func (b *Builder) buildOlric() error {
	fmt.Printf("[3/8] Cross-compiling Olric %s...\n", constants.OlricVersion)
	return b.buildPinned(filepath.Join(b.projectDir, thirdPartyDir, "olric"),
		"github.com/olric-data/olric/cmd/olric-server", "olric-server")
}

func (b *Builder) buildIPFSCluster() error {
	fmt.Printf("[4/8] Cross-compiling IPFS Cluster %s...\n", constants.IPFSClusterVersion)
	return b.buildPinned(filepath.Join(b.projectDir, thirdPartyDir, "ipfs-cluster"),
		"github.com/ipfs-cluster/ipfs-cluster/cmd/ipfs-cluster-service", "ipfs-cluster-service")
}

func (b *Builder) buildCaddy() error {
	fmt.Printf("[6/8] Building Caddy %s with the Orama modules...\n", constants.CaddyVersion)
	moduleDir := filepath.Join(b.projectDir, "..", "caddy")
	if _, err := os.Stat(filepath.Join(moduleDir, "go.mod")); err != nil {
		return fmt.Errorf("the Caddy modules are missing at %s (expected the repository's caddy/ directory): %w", moduleDir, err)
	}
	return b.buildPinned(moduleDir, caddyMainPackage, "caddy")
}
