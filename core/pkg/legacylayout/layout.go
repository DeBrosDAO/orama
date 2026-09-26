// Package legacylayout carries a node from the on-disk layout its gateways and
// namespace units used before this release onto the current one.
//
// On 0.122.x the index gateway ran inside orama-node and every gateway took the
// orama directory as its data directory, so they wrote secrets/jwt-*.pem,
// sqlite/, deployments/ and configs/turn.yaml straight under it, and the
// namespace units read their env files from data/namespaces/<ns>/<svc>.env.
// Pre-release builds of this line also wrote deployment environments and
// tokens to deployment-env/ (0.122.x kept them inline in each deployment's
// unit). Now every gateway is
// orama-namespace-gateway@<ns> and writes only under data/, the env files live
// in the root-owned tree pkg/unitenv describes, and deployment environments and
// tokens live in the root-only directory pkg/deploysecrets describes.
//
// orama-node performs the move itself, as the orama user, before it starts or
// regenerates any namespace service. Root never walks, renames or chowns
// anything here: every path involved sits in the orama-owned tree, and a root
// process operating on it would follow whatever symlinks the orama user planted
// (the reason the earlier root-run migration was removed). What only root may
// write — the unit env tree and the deployment secrets — is handed to
// orama-privhelper as contents, never as paths.
package legacylayout

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

// Where the old layout kept things, relative to the orama directory.
const (
	secretsSubdir       = "secrets"
	configsSubdir       = "configs"
	deploymentEnvSubdir = "deployment-env"

	// envFileSuffix ends every namespace unit env file.
	envFileSuffix = ".env"
)

// turnEnvFileName is the env of a per-namespace TURN server, the kind the
// shared one replaced.
const turnEnvFileName = "turn" + envFileSuffix

// SigningKeyNames are the index gateway's signing keys, which the old layout
// kept in secrets/ under the same names they have in a gateway's state
// directory.
var SigningKeyNames = []string{constants.GatewayRSAKeyFileName, constants.GatewayEdDSAKeyFileName}

// SecretsDir is <oramaDir>/secrets, where the old layout kept the index
// gateway's signing keys. install writes it as root.
func SecretsDir(oramaDir string) string {
	return filepath.Join(oramaDir, secretsSubdir)
}

// IndexGatewayStateDir is where the index gateway's signing keys live now.
func IndexGatewayStateDir(oramaDir string) string {
	return constants.GatewayStateDir(constants.NamespacesDir(oramaDir), constants.IndexNamespace)
}

// TURNConfigPath is <oramaDir>/configs/turn.yaml, the shared TURN config's old
// location.
func TURNConfigPath(oramaDir string) string {
	return filepath.Join(oramaDir, configsSubdir, constants.TURNConfigFileName)
}

// DeploymentEnvDir is <oramaDir>/deployment-env, where gateways wrote
// orama-deploy-<instance>.{env,token}.
func DeploymentEnvDir(oramaDir string) string {
	return filepath.Join(oramaDir, deploymentEnvSubdir)
}

// NamespaceEnvDir is the old env-file tree: <NamespaceEnvDir>/<ns>/<svc>.env.
// It has the same shape as unitenv.Dir, so a reader that takes the tree's root
// reads either layout.
func NamespaceEnvDir(oramaDir string) string {
	return constants.NamespacesDir(oramaDir)
}

// HasTURN reports whether the old layout records a TURN server on this node:
// the shared config at configs/turn.yaml, or a per-namespace turn.env from
// before the shared server existed.
func HasTURN(oramaDir string) (bool, error) {
	if ok, err := exists(TURNConfigPath(oramaDir)); err != nil || ok {
		return ok, err
	}
	return anyNamespaceEnv(NamespaceEnvDir(oramaDir), turnEnvFileName)
}

// anyNamespaceEnv reports whether any namespace under envDir
// (<envDir>/<ns>/<name>) has an env file called name. A tree that does not
// exist has none; one that cannot be read is an error, not a "no".
//
// Root calls this (the upgrade's firewall phase) on a directory the orama user
// owns, so it is opened as a directory without following a symlink and
// without blocking: a FIFO or symlink planted in its place fails at once
// instead of hanging the upgrade with the node's services stopped. The
// entries are only Lstat'ed, never opened.
func anyNamespaceEnv(envDir, name string) (bool, error) {
	dir, err := os.OpenFile(envDir, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("open %s as a directory: %w", envDir, err)
	}
	defer dir.Close()
	namespaces, err := dir.ReadDir(-1)
	if err != nil {
		return false, fmt.Errorf("list %s: %w", envDir, err)
	}
	for _, ns := range namespaces {
		if !ns.IsDir() {
			continue
		}
		if ok, err := exists(filepath.Join(envDir, ns.Name(), name)); err != nil || ok {
			return ok, err
		}
	}
	return false, nil
}

// exists reports whether path exists, without following a symlink.
func exists(path string) (bool, error) {
	_, err := os.Lstat(path)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	default:
		return false, fmt.Errorf("inspect %s: %w", path, err)
	}
}

// isEnvFileName reports whether name is a namespace unit env file.
func isEnvFileName(name string) bool {
	return strings.HasSuffix(name, envFileSuffix)
}
