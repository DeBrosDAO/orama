package netregistry

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// sourceFile records, in a stored network, the URL its manifest came from.
	sourceFile    = "source"
	storeDirPerm  = 0o700
	storeFilePerm = 0o600
)

// Store keeps the networks an operator added by URL, one directory each.
type Store struct {
	// Dir is the directory the networks live in; it need not exist yet.
	Dir string
}

// Load reads every stored network. A network that fails to load is an error,
// not a gap: a custom network that silently vanished would leave the operator
// believing they were on a chain they are not.
func (s Store) Load() (*Registry, error) {
	r := &Registry{nets: map[string]*Network{}}
	entries, err := os.ReadDir(s.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the network store %s: %w", s.Dir, err)
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		n, err := s.loadOne(e.Name())
		if err != nil {
			return nil, fmt.Errorf("stored network %q: %w (remove it with `orama network remove %s`)", e.Name(), err, e.Name())
		}
		r.nets[n.Manifest.Name] = n
	}
	return r, nil
}

func (s Store) loadOne(name string) (*Network, error) {
	dir := filepath.Join(s.Dir, name)
	n, err := loadNetworkDir(os.DirFS(dir), ".", name)
	if err != nil {
		return nil, err
	}
	source, err := os.ReadFile(filepath.Join(dir, sourceFile))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", sourceFile, err)
	}
	n.Source = strings.TrimSpace(string(source))
	return n, nil
}

// Save stores n, replacing a stored network of the same name. The network is
// written whole beside its final place and renamed into it, so a reader sees
// the old network or the new one.
func (s Store) Save(n *Network) error {
	name := n.Manifest.Name
	if !nameRE.MatchString(name) {
		return fmt.Errorf("network name %q cannot be stored", name)
	}
	manifest, err := n.Manifest.Marshal()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.Dir, storeDirPerm); err != nil {
		return fmt.Errorf("create the network store %s: %w", s.Dir, err)
	}
	tmp, err := os.MkdirTemp(s.Dir, "."+name+".tmp-")
	if err != nil {
		return fmt.Errorf("create a temporary network directory: %w", err)
	}
	defer os.RemoveAll(tmp)
	for file, data := range map[string][]byte{
		ManifestFile:    manifest,
		ReleaseRootFile: n.Root,
		sourceFile:      []byte(n.Source + "\n"),
	} {
		if err := os.WriteFile(filepath.Join(tmp, file), data, storeFilePerm); err != nil {
			return fmt.Errorf("write %s of network %s: %w", file, name, err)
		}
	}
	final := filepath.Join(s.Dir, name)
	if err := os.RemoveAll(final); err != nil {
		return fmt.Errorf("replace network %s: %w", name, err)
	}
	if err := os.Rename(tmp, final); err != nil {
		return fmt.Errorf("store network %s: %w", name, err)
	}
	return nil
}

// Remove deletes a stored network and reports whether there was one.
func (s Store) Remove(name string) (bool, error) {
	if !nameRE.MatchString(name) {
		return false, nil
	}
	dir := filepath.Join(s.Dir, name)
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err := os.RemoveAll(dir); err != nil {
		return false, fmt.Errorf("remove network %s: %w", name, err)
	}
	return true, nil
}
