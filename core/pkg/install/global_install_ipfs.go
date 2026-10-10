package install

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/globalnetns"
	"github.com/DeBrosOfficial/network/pkg/install/installers"
)

const (
	// ipfsRepoDirMode lets the RPC group traverse the repo directory to read the token.
	ipfsRepoDirMode = 0o750
	// ipfsTokenBytes bounds the token file read back on a second run.
	ipfsTokenBytes = 4096
	// ipfsConfigLimit bounds the repo config read back.
	ipfsConfigLimit = 4 << 20
	// ipfsPrivateFileMode is the config and the GC environment: the Kubo user's alone.
	ipfsPrivateFileMode = 0o600
)

// installPublicKubo prepares the public Kubo repo: it checks the staged ipfs
// is the pinned Kubo version, runs `ipfs init --profile=server` as the Kubo
// account when there is no repo, then writes the public config (no swarm.key,
// private ranges filtered, RPC behind a bearer), the token and the GC
// environment. The repo is the Kubo account's; the token is readable by the
// group orama-ipfs-pub-rpc, which the provider belongs to. A second run keeps
// the repo's identity and its token.
func installPublicKubo(h GlobalHost, storageBytes uint64, colocated bool) error {
	repo := filepath.Join(h.StateDir, filepath.Base(constants.GlobalIPFSHome))
	uid, gid, err := h.Lookup(globalIPFSUser)
	if err != nil {
		return err
	}
	rpcGID, err := h.LookupGroup(globalIPFSRPCGroup)
	if err != nil {
		return err
	}
	if err := checkKuboVersion(h); err != nil {
		return err
	}
	if err := prepareKuboRepo(h, repo, uid, rpcGID); err != nil {
		return err
	}
	existing, err := h.StateRoot.ReadFile(filepath.Join(repo, "config"), ipfsConfigLimit)
	if err != nil {
		return fmt.Errorf("read the public Kubo config in %s: %w", repo, err)
	}
	token, err := kuboToken(h, repo)
	if err != nil {
		return err
	}
	if err := installers.WritePublicKuboFiles(h.StateRoot, repo, token, storageBytes, existing, globalnetns.KuboAPIHost(colocated)); err != nil {
		return err
	}
	env := "IPFS_API_AUTH=bearer:" + token + "\n"
	if err := h.StateRoot.WriteFile(filepath.Join(repo, globalIPFSGCEnvFile), []byte(env), ipfsPrivateFileMode); err != nil {
		return fmt.Errorf("write the public Kubo GC environment: %w", err)
	}
	return ownKuboFiles(h, repo, uid, gid, rpcGID)
}

// checkKuboVersion runs the staged ipfs as the Kubo account, not as root, and
// requires the version the cluster is pinned to.
func checkKuboVersion(h GlobalHost) error {
	out, err := h.Run("runuser", "-u", globalIPFSUser, "--", filepath.Join(h.BinDir, globalKuboBinary), "--version")
	if err != nil {
		return fmt.Errorf("run the staged ipfs --version as %s: %w\n%s", globalIPFSUser, err, strings.TrimSpace(string(out)))
	}
	want := strings.TrimPrefix(constants.IPFSKuboVersion, "v")
	if !strings.Contains(string(out), "ipfs version "+want) {
		return fmt.Errorf("the staged ipfs reports %q, not Kubo %s; stage the release's ipfs", strings.TrimSpace(string(out)), constants.IPFSKuboVersion)
	}
	return nil
}

// prepareKuboRepo creates the repo directory for the Kubo account and the RPC
// group, and initialises it when it has no config.
func prepareKuboRepo(h GlobalHost, repo string, uid, rpcGID int) error {
	if err := h.StateRoot.MkdirAll(repo, ipfsRepoDirMode); err != nil {
		return fmt.Errorf("create %s: %w", repo, err)
	}
	if err := h.Chown(h.StateRoot, repo, uid, rpcGID); err != nil {
		return fmt.Errorf("chown %s: %w", repo, err)
	}
	if err := h.StateRoot.Chmod(repo, ipfsRepoDirMode); err != nil {
		return fmt.Errorf("chmod %s: %w", repo, err)
	}
	_, err := h.StateRoot.ReadFile(filepath.Join(repo, "config"), ipfsConfigLimit)
	if err == nil {
		return nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("check the public Kubo repo %s: %w", repo, err)
	}
	out, err := h.Run("runuser", "-u", globalIPFSUser, "--", "env", "HOME="+repo, "IPFS_PATH="+repo,
		filepath.Join(h.BinDir, globalKuboBinary), "init", "--profile=server", "--repo-dir="+repo)
	if err != nil {
		return fmt.Errorf("ipfs init as %s: %w\n%s", globalIPFSUser, err, strings.TrimSpace(string(out)))
	}
	h.Logf("  ✓ public Kubo repo %s initialised", repo)
	return nil
}

// kuboToken is the repo's RPC token, or a new one when it has none.
func kuboToken(h GlobalHost, repo string) (string, error) {
	raw, err := h.StateRoot.ReadFile(filepath.Join(repo, installers.PublicAPITokenFile), ipfsTokenBytes)
	if err == nil {
		if token := strings.TrimSpace(string(raw)); token != "" {
			return token, nil
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("read the public Kubo token: %w", err)
	}
	return installers.NewPublicKuboToken()
}

// ownKuboFiles gives the config and GC environment to the Kubo account alone
// and the token to it and the RPC group.
func ownKuboFiles(h GlobalHost, repo string, uid, gid, rpcGID int) error {
	files := []struct {
		name string
		gid  int
		mode fs.FileMode
	}{
		{"config", gid, ipfsPrivateFileMode},
		{globalIPFSGCEnvFile, gid, ipfsPrivateFileMode},
		{installers.PublicAPITokenFile, rpcGID, 0o640},
		{installers.PublicDenylistFile, rpcGID, 0o640},
	}
	for _, f := range files {
		path := filepath.Join(repo, f.name)
		if err := h.Chown(h.StateRoot, path, uid, f.gid); err != nil {
			return fmt.Errorf("chown %s: %w", path, err)
		}
		if err := h.StateRoot.Chmod(path, f.mode); err != nil {
			return fmt.Errorf("chmod %s: %w", path, err)
		}
	}
	return nil
}
