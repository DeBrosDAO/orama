package legacylayout

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/unitenv"
)

// Stager hands what only root may write to orama-privhelper: contents, never
// paths. PrivHelperStager is the node's; tests pass a fake.
type Stager interface {
	SetUnitEnv(namespace, service, contents string) error
	SetDeploymentEnv(instance, contents string) error
	SetDeploymentToken(instance, token string) error
}

// Migrator moves one node's old-layout state into the current layout.
type Migrator struct {
	// OramaDir is the install root (/opt/orama/.orama on a node).
	OramaDir string
	// UnitEnvDir is the unit env tree (unitenv.Dir on a node). The orama
	// group may read it, which is how an env file already staged is told
	// apart from one that is not.
	UnitEnvDir string
	// SystemdUnitDir is where 0.122.x wrote each deployment's unit
	// (SystemdUnitDir when empty); the environment is read out of it.
	SystemdUnitDir string
	Stager         Stager
	Logf           func(format string, args ...any)
}

// dataTreeDirMode is the mode of a directory created to hold a moved tree
// (data/turn); what is moved keeps its own modes.
const dataTreeDirMode = 0o700

// maxLegacyFileSize bounds what is read from a legacy env, token or key file.
// Each is a few hundred bytes; anything larger is not one of them.
const maxLegacyFileSize = 1 << 20

// Run moves everything the old layout holds.
//
// For each path: only the old one exists → moved (or, for what only root may
// write, staged through the helper and then deleted); only the new one, or
// neither → nothing to do; both → Run fails naming both. Two copies of a
// signing key, a tenant's database or a unit's environment are not something
// to choose between automatically. Every path is checked before anything is
// changed, so a refusal leaves the node exactly as it was, and running it
// again after it succeeded does nothing.
func (m Migrator) Run() error {
	if m.UnitEnvDir == "" {
		m.UnitEnvDir = unitenv.Dir
	}
	if m.Logf == nil {
		m.Logf = func(string, ...any) {}
	}
	keys, err := m.planSigningKeys()
	if err != nil {
		return err
	}
	trees, err := m.planTreeMoves()
	if err != nil {
		return err
	}
	deploys, err := m.planDeploymentDirs()
	if err != nil {
		return err
	}
	envs, err := m.planUnitEnvs()
	if err != nil {
		return err
	}
	deployFiles, err := m.planDeploymentFiles()
	if err != nil {
		return err
	}

	if err := m.applySigningKeys(keys); err != nil {
		return err
	}
	for _, mv := range trees {
		if err := mv.apply(); err != nil {
			return err
		}
		m.Logf("moved %s to %s", mv.from, mv.to)
	}
	if err := m.applyDeploymentDirs(deploys); err != nil {
		return err
	}
	if err := m.applyUnitEnvs(envs); err != nil {
		return err
	}
	return m.applyDeploymentFiles(deployFiles)
}

// move renames one old-layout path into the current layout.
type move struct {
	from, to   string
	parentMode os.FileMode
	wantDir    bool
}

// planTreeMoves lists the trees the old layout wrote under the orama
// directory. The TURN config is moved, not deleted: the upgrade's firewall
// step keeps the relay range open on a node where it exists. Deployments are
// not a tree move: the old layout nested them by namespace and name
// (planDeploymentDirs).
func (m Migrator) planTreeMoves() ([]move, error) {
	candidates := []move{
		{filepath.Join(m.OramaDir, constants.SQLiteSubdir), constants.SQLiteBaseDir(m.OramaDir), dataTreeDirMode, true},
		{TURNConfigPath(m.OramaDir), constants.HostTURNConfigPath(m.OramaDir), dataTreeDirMode, false},
	}
	var pending []move
	var conflicts []error
	for _, mv := range candidates {
		todo, err := mv.check()
		switch {
		case err != nil:
			conflicts = append(conflicts, err)
		case todo:
			pending = append(pending, mv)
		}
	}
	return pending, errors.Join(conflicts...)
}

// check reports whether mv's source is still on the old layout.
func (mv move) check() (bool, error) {
	info, err := os.Lstat(mv.from)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect %s: %w", mv.from, err)
	}
	want := "regular file"
	if mv.wantDir {
		want = "directory"
	}
	if mv.wantDir && !info.IsDir() || !mv.wantDir && !info.Mode().IsRegular() {
		return false, fmt.Errorf("%s is not the %s the old layout wrote (mode %s); move it aside and orama-node retries on its own",
			mv.from, want, info.Mode())
	}
	toExists, err := exists(mv.to)
	if err != nil {
		return false, err
	}
	if toExists {
		return false, bothLayoutsError(mv.from, mv.to)
	}
	return true, nil
}

func (mv move) apply() error {
	if err := os.MkdirAll(filepath.Dir(mv.to), mv.parentMode); err != nil {
		return fmt.Errorf("create %s for %s: %w", filepath.Dir(mv.to), mv.to, err)
	}
	if err := os.Rename(mv.from, mv.to); err != nil {
		return fmt.Errorf("move %s to %s: %w", mv.from, mv.to, err)
	}
	return nil
}

// bothLayoutsError is the refusal when a path exists on both layouts.
func bothLayoutsError(oldPath, newPath string) error {
	return fmt.Errorf("both %s (old layout) and %s (current layout) exist; refusing to merge or overwrite either — "+
		"keep the one this node should use, move the other out of both places, and orama-node retries on its own", oldPath, newPath)
}

// readNoFollow reads a regular file without following a symlink at path.
// O_NONBLOCK keeps a FIFO planted there from blocking the open; the file type
// is checked before anything is read.
func readNoFollow(path string) ([]byte, os.FileMode, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, 0, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, 0, fmt.Errorf("stat %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, 0, fmt.Errorf("%s is not a regular file", path)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxLegacyFileSize+1))
	if err != nil {
		return nil, 0, fmt.Errorf("read %s: %w", path, err)
	}
	if len(data) > maxLegacyFileSize {
		return nil, 0, fmt.Errorf("%s is larger than %d bytes, which no file of the old layout is", path, maxLegacyFileSize)
	}
	return data, info.Mode().Perm(), nil
}

// writeFileAtomic writes data to path through a fresh temp file in the same
// directory, with mode, and renames it into place.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create a temp file for %s: %w", path, err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", tmpPath, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmpPath, err)
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		return fmt.Errorf("chmod %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("install %s: %w", path, err)
	}
	return syncDir(filepath.Dir(path))
}

// syncDir makes the entries just renamed into dir durable, so the order in
// which two files were written survives a power loss.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open %s to sync it: %w", dir, err)
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("sync %s: %w", dir, err)
	}
	return nil
}
