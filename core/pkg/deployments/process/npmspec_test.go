package process

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestCheckRegistryOnlyDependencies_acceptsRegistryPackages(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", `{
		"name": "app",
		"dependencies": {
			"express": "^4.19.2", "lodash": "4.17.21", "a": "~1.2", "b": ">=1 <2 || ^3.0.0-beta.1",
			"c": "latest", "d": "*", "e": "", "f": "1.x", "g": "1.0.0 - 2.0.0",
			"h": "npm:@scope/real-name@^2.0.0", "i": "npm:other"
		},
		"devDependencies": {"typescript": "^5.4.0"},
		"optionalDependencies": null,
		"overrides": {"foo": "1.0.0", "bar": {".": "2.0.0", "baz": "$express"}}
	}`)
	writeFile(t, dir, "package-lock.json", `{
		"lockfileVersion": 3,
		"packages": {
			"": {"name": "app"},
			"node_modules/express": {"version": "4.19.2", "resolved": "https://registry.npmjs.org/express/-/express-4.19.2.tgz"},
			"node_modules/bundled": {"version": "1.0.0", "inBundle": true}
		},
		"dependencies": {
			"express": {"version": "4.19.2"},
			"h": {"version": "npm:@scope/real-name@2.1.0", "resolved": "https://registry.npmjs.org/@scope/real-name/-/real-name-2.1.0.tgz"}
		}
	}`)
	if err := CheckRegistryOnlyDependencies(dir); err != nil {
		t.Fatalf("a registry-only app was refused: %v", err)
	}
}

// A git dependency makes npm run git; a URL makes it fetch from anywhere;
// file: and link: point it at the filesystem.
func TestCheckRegistryOnlyDependencies_refusesNonRegistrySpecs(t *testing.T) {
	for _, spec := range []string{
		"github:user/repo", "user/repo", "git+ssh://git@github.com/u/r.git", "git://github.com/u/r",
		"https://evil.example/x.tgz", "http://10.0.0.1/x.tgz", "file:../x", "link:../x", "../x",
		"npm:x@github:u/r", "npm:../x", "gitlab:u/r", "workspace:*",
		// npm reads these as local directories.
		".", "..", ".x", " ..", "npm:x@..",
	} {
		for _, field := range dependencyFields {
			dir := t.TempDir()
			writeFile(t, dir, "package.json", `{"`+field+`":{"x":"`+spec+`"}}`)
			if err := CheckRegistryOnlyDependencies(dir); err == nil {
				t.Errorf("%s %q was accepted", field, spec)
			}
		}
	}
	for name, manifest := range map[string]string{
		"override":        `{"overrides":{"x":"github:u/r"}}`,
		"nested override": `{"overrides":{"x":{"y":"file:../y"}}}`,
		"workspaces":      `{"workspaces":["packages/*"]}`,
		"not an object":   `{"dependencies":["x"]}`,
		"not JSON":        `{`,
	} {
		dir := t.TempDir()
		writeFile(t, dir, "package.json", manifest)
		if err := CheckRegistryOnlyDependencies(dir); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestCheckRegistryOnlyDependencies_refusesNonRegistryLockEntries(t *testing.T) {
	for name, lock := range map[string]string{
		"git resolved":  `{"packages":{"node_modules/x":{"resolved":"git+ssh://git@github.com/u/r.git#abc"}}}`,
		"file resolved": `{"packages":{"node_modules/x":{"resolved":"file:../x"}}}`,
		"link":          `{"packages":{"node_modules/x":{"resolved":"../x","link":true}}}`,
		"http resolved": `{"packages":{"node_modules/x":{"resolved":"http://10.0.0.1/x.tgz"}}}`,
		"v1 git":        `{"lockfileVersion":1,"dependencies":{"x":{"version":"github:u/r"}}}`,
		"v1 nested":     `{"lockfileVersion":1,"dependencies":{"x":{"version":"1.0.0","dependencies":{"y":{"version":"file:../y"}}}}}`,
		"v1 directory":  `{"lockfileVersion":1,"dependencies":{"x":{"version":".."}}}`,
	} {
		for _, file := range lockfileNames {
			dir := t.TempDir()
			writeFile(t, dir, "package.json", `{"dependencies":{"x":"^1.0.0"}}`)
			writeFile(t, dir, file, lock)
			if err := CheckRegistryOnlyDependencies(dir); err == nil {
				t.Errorf("%s in %s was accepted", name, file)
			}
		}
	}
}

// The manifest and lockfile are the tenant's: read without following a
// symlink, never blocking on a FIFO, and bounded.
func TestCheckRegistryOnlyDependencies_readsOnlyRegularFiles(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "secret.json")
	writeFile(t, filepath.Dir(outside), "secret.json", `{"dependencies":{}}`)

	dir := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "package.json")); err != nil {
		t.Fatal(err)
	}
	if err := CheckRegistryOnlyDependencies(dir); err == nil {
		t.Error("a symlinked package.json was followed")
	}

	dir = t.TempDir()
	writeFile(t, dir, "package.json", `{}`)
	if err := syscall.Mkfifo(filepath.Join(dir, "package-lock.json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CheckRegistryOnlyDependencies(dir); err == nil {
		t.Error("a FIFO lockfile was accepted")
	}

	dir = t.TempDir()
	writeFile(t, dir, "package.json", `{"x":"`+strings.Repeat("a", maxManifestBytes)+`"}`)
	if err := CheckRegistryOnlyDependencies(dir); err == nil {
		t.Error("an oversized package.json was accepted")
	}

	if err := CheckRegistryOnlyDependencies(t.TempDir()); err == nil || !strings.Contains(err.Error(), "package.json") {
		t.Errorf("a missing package.json: %v", err)
	}
}
