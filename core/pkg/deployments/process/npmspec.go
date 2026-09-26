package process

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
)

// What orama-deploy-build@ will install, checked before it starts.
//
// --ignore-scripts stops lifecycle scripts, and the build pins npm to the
// public registry. What neither covers is a dependency that is not a registry
// package at all: a git dependency makes npm run git (and, in some npm
// versions, the dependency's prepare script to build it), a URL makes it fetch
// from wherever the URL says, and file: or link: points it at the filesystem.
// None is something a server-side install should do on a tenant's behalf, so
// all of them are refused here, in package.json and in the lockfile, with a
// message saying to ship node_modules instead.

const (
	// maxManifestBytes caps package.json. Real ones are a few KiB.
	maxManifestBytes = 1 << 20
	// maxLockfileBytes caps package-lock.json / npm-shrinkwrap.json. Large
	// applications' lockfiles run to a few MiB.
	maxLockfileBytes = 64 << 20
)

// dependencyFields are the package.json fields npm resolves. devDependencies
// are included: with --omit=dev npm still resolves them to build its tree,
// which for a git dependency means running git.
var dependencyFields = []string{"dependencies", "devDependencies", "optionalDependencies", "peerDependencies"}

// lockfileNames are the lockfiles the build copies next to package.json.
var lockfileNames = []string{"package-lock.json", "npm-shrinkwrap.json"}

var (
	// registryRange is a semver range or a dist-tag: everything npm resolves
	// against the registry, and nothing with a ':' or '/' — no URL, git
	// shorthand (user/repo, github:…), file: or link:.
	registryRange = regexp.MustCompile(`^[A-Za-z0-9 .^~<>=|*+_-]*$`)
	// packageName is an npm package name, scoped or not.
	packageName = regexp.MustCompile(`^(@[A-Za-z0-9][A-Za-z0-9._~-]*/)?[A-Za-z0-9][A-Za-z0-9._~-]*$`)
	// overrideReference is an override naming a direct dependency's version.
	overrideReference = regexp.MustCompile(`^\$(@[A-Za-z0-9][A-Za-z0-9._~-]*/)?[A-Za-z0-9][A-Za-z0-9._~-]*$`)
)

// registryLockPrefix is what a lockfile's resolved URL must start with. The
// host is rewritten to the pinned registry by npm_config_replace_registry_host
// in the build unit; the scheme is what separates a tarball from git or a path.
const registryLockPrefix = "https://"

// CheckRegistryOnlyDependencies refuses a deployment in dir whose package.json
// or lockfile names anything but registry packages.
func CheckRegistryOnlyDependencies(dir string) error {
	manifest, err := ReadManifest(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("the deployment has no package.json to install from")
	}
	if err != nil {
		return err
	}
	if err := checkManifest(manifest); err != nil {
		return fmt.Errorf("package.json: %w; ship node_modules in the tarball for dependencies that are not on the npm registry", err)
	}
	for _, name := range lockfileNames {
		lock, err := readRegularFile(filepath.Join(dir, name), maxLockfileBytes)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if err := checkLockfile(lock); err != nil {
			return fmt.Errorf("%s: %w; ship node_modules in the tarball for dependencies that are not on the npm registry", name, err)
		}
	}
	return nil
}

// ReadManifest reads dir's package.json the way the dependency check does:
// a regular file, not followed through a symlink, never a FIFO that would
// block, at most maxManifestBytes. fs.ErrNotExist means there is none.
func ReadManifest(dir string) ([]byte, error) {
	return readRegularFile(filepath.Join(dir, "package.json"), maxManifestBytes)
}

// readRegularFile reads path, refusing a symlink or anything over limit. The
// file is the tenant's: a symlink would have this process read whatever it
// points at.
func readRegularFile(path string, limit int64) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		return nil, fmt.Errorf("%s must be a regular file in the deployment: %w", filepath.Base(path), err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect %s: %w", filepath.Base(path), err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s must be a regular file in the deployment, not %v", filepath.Base(path), info.Mode().Type())
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s is over %d bytes", filepath.Base(path), limit)
	}
	return data, nil
}

func checkManifest(data []byte) error {
	var manifest map[string]json.RawMessage
	if err := json.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("not valid JSON: %w", err)
	}
	if ws, ok := manifest["workspaces"]; ok && string(ws) != "null" {
		return fmt.Errorf("workspaces are not supported by the server-side install: they link local directories")
	}
	for _, field := range dependencyFields {
		raw, ok := manifest[field]
		if !ok || string(raw) == "null" {
			continue
		}
		var deps map[string]string
		if err := json.Unmarshal(raw, &deps); err != nil {
			return fmt.Errorf("%s is not an object of name to version: %w", field, err)
		}
		for _, name := range sortedKeys(deps) {
			if err := checkDependencySpec(deps[name]); err != nil {
				return fmt.Errorf("%s %q: %w", field, name, err)
			}
		}
	}
	if raw, ok := manifest["overrides"]; ok {
		if err := checkOverrides(raw, "overrides"); err != nil {
			return err
		}
	}
	return nil
}

// checkDependencySpec accepts a registry range or tag, or an npm: alias of a
// registry package.
func checkDependencySpec(spec string) error {
	if isRegistryRange(spec) {
		return nil
	}
	if alias, ok := strings.CutPrefix(spec, "npm:"); ok {
		name, rng := alias, ""
		if at := strings.LastIndex(alias, "@"); at > 0 {
			name, rng = alias[:at], alias[at+1:]
		}
		if packageName.MatchString(name) && isRegistryRange(rng) {
			return nil
		}
	}
	return fmt.Errorf("%q is not a registry version; git, URL, file: and link: dependencies are refused", spec)
}

// isRegistryRange reports whether spec is a range or tag npm resolves against
// the registry. The character set alone is not enough: npm reads a spec that
// starts with '.' — ".", "..", ".x" — as a local directory.
func isRegistryRange(spec string) bool {
	return registryRange.MatchString(spec) && !strings.HasPrefix(strings.TrimLeft(spec, " "), ".")
}

// checkOverrides walks an overrides object, whose values are specs or nested
// objects ("." names the package's own version).
func checkOverrides(raw json.RawMessage, path string) error {
	var spec string
	if err := json.Unmarshal(raw, &spec); err == nil {
		if overrideReference.MatchString(spec) {
			return nil
		}
		if err := checkDependencySpec(spec); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		return nil
	}
	var nested map[string]json.RawMessage
	if err := json.Unmarshal(raw, &nested); err != nil {
		return fmt.Errorf("%s is neither a version nor an object", path)
	}
	for _, key := range sortedKeys(nested) {
		if err := checkOverrides(nested[key], path+"."+key); err != nil {
			return err
		}
	}
	return nil
}

// lockEntry is what a lockfile records about one installed package, in the
// v2/v3 "packages" map and the v1 "dependencies" tree alike.
type lockEntry struct {
	Version      string               `json:"version"`
	Resolved     string               `json:"resolved"`
	Link         bool                 `json:"link"`
	Dependencies map[string]lockEntry `json:"dependencies"`
}

func checkLockfile(data []byte) error {
	var lock struct {
		Packages     map[string]lockEntry `json:"packages"`
		Dependencies map[string]lockEntry `json:"dependencies"`
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		return fmt.Errorf("not a valid lockfile: %w", err)
	}
	for _, path := range sortedKeys(lock.Packages) {
		if path == "" {
			continue // the project itself
		}
		if err := checkLockEntry(path, lock.Packages[path], false); err != nil {
			return err
		}
	}
	return checkLockTree(lock.Dependencies)
}

// checkLockTree walks a v1 lockfile's nested dependencies.
func checkLockTree(deps map[string]lockEntry) error {
	for _, name := range sortedKeys(deps) {
		if err := checkLockEntry(name, deps[name], true); err != nil {
			return err
		}
		if err := checkLockTree(deps[name].Dependencies); err != nil {
			return err
		}
	}
	return nil
}

// checkLockEntry refuses a link, a resolved location that is not an https
// tarball, and — in a v1 lockfile, where it holds the spec — a version that
// is not one.
func checkLockEntry(path string, e lockEntry, v1 bool) error {
	if e.Link {
		return fmt.Errorf("%s is a link to a local directory", path)
	}
	if e.Resolved != "" && !strings.HasPrefix(e.Resolved, registryLockPrefix) {
		return fmt.Errorf("%s resolves to %q, not a registry tarball", path, e.Resolved)
	}
	if v1 && e.Version != "" {
		if err := checkDependencySpec(e.Version); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
