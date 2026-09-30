//go:build e2e_fleet

package realistic

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/cliconf"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// The reference applications under e2e/apps (each Go one is a module of its
// own, so the e2e module never builds them).
const (
	AppStatic   = "static-site"
	AppNextSSR  = "next-ssr"
	AppNodeAPI  = "node-api"
	AppGo       = "go-backend"
	AppWASM     = "wasm-bundle"
	AppChatWeb  = "chat-web"
	AppCallWeb  = "call-web"
	appsDir     = "e2e/apps"
	appFileMode = 0o644
	appDirMode  = 0o755
	// CAFile is the name a server-side app loads the cluster's trust from.
	CAFile = "ca.pem"
	// ReleaseMarker is replaced in an app's sources to tell versions apart.
	ReleaseMarker = "RELEASE_MARKER"
)

// CopyApp copies e2e/apps/<name> into a fresh directory, replacing every
// occurrence of each key of replace in its text files, and adds extra files
// (relative path -> content). It returns the copy's directory.
func CopyApp(t testing.TB, name string, replace map[string]string, extra map[string][]byte) string {
	t.Helper()
	src := filepath.Join(cliconf.RepoRoot(t), filepath.FromSlash(appsDir), name)
	dst := filepath.Join(t.TempDir(), name)
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, appDirMode)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		body := string(raw)
		for from, to := range replace {
			body = strings.ReplaceAll(body, from, to)
		}
		return os.WriteFile(target, []byte(body), appFileMode)
	})
	if err != nil {
		t.Fatalf("failed to copy the reference app %s: %v", name, err)
	}
	for rel, body := range extra {
		if err := os.WriteFile(filepath.Join(dst, filepath.FromSlash(rel)), body, appFileMode); err != nil {
			t.Fatalf("failed to add %s to %s: %v", rel, name, err)
		}
	}
	return dst
}

// ClusterCA is the run's pinned CA bundle: what a server-side reference app
// must trust to reach its gateway over TLS on a fleet whose certificates come
// from Let's Encrypt staging (docs/SANDBOX.md).
func ClusterCA(t testing.TB, f *fleet.Fleet) []byte {
	t.Helper()
	raw, err := os.ReadFile(f.State.CAFile)
	if err != nil {
		t.Fatalf("failed to read the run's CA bundle %s: %v", f.State.CAFile, err)
	}
	return raw
}

// ServerApp copies a server-side reference app with the run's CA added as
// ca.pem and marker in place of RELEASE_MARKER.
func ServerApp(t testing.TB, f *fleet.Fleet, name, marker string) string {
	t.Helper()
	return CopyApp(t, name, map[string]string{ReleaseMarker: marker}, map[string][]byte{CAFile: ClusterCA(t, f)})
}
