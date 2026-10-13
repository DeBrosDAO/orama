package install

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rootfs"
)

var fakeIDs = accountIDs{
	uid: func(name string) (int, error) {
		if name == "orama" {
			return 990, nil
		}
		return 0, errors.New("no user " + name)
	},
	gid: func(name string) (int, error) {
		if name == "orama-sfu" {
			return 991, nil
		}
		return 0, errors.New("no group " + name)
	},
}

func writeTree(t *testing.T, oramaDir string, files ...string) {
	t.Helper()
	for _, f := range files {
		path := filepath.Join(oramaDir, f)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestServiceOwnershipPlan_handsEverySFUConfigToTheSFUGroup(t *testing.T) {
	oramaDir := filepath.Join(t.TempDir(), ".orama")
	writeTree(t, oramaDir,
		"data/namespaces/anchat/configs/sfu-node-1.yaml",
		"data/namespaces/other/configs/sfu-node-2.yaml",
		"data/namespaces/anchat/configs/gateway-node-1.yaml",
		"data/namespaces/anchat/configs/olric-node-1.yaml",
		"data/namespaces/anchat/configs/sfu-node-1.yaml.tmp-123",
		"configs/sfu-node-1.yaml",
	)
	changes, err := serviceOwnershipPlan(oramaDir, fakeIDs)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, c := range changes {
		paths = append(paths, c.path)
		if c.uid != 990 || c.gid != 991 || c.mode != 0o640 {
			t.Errorf("%s: %d:%d %04o, want 990:991 0640", c.path, c.uid, c.gid, c.mode)
		}
	}
	sort.Strings(paths)
	want := []string{
		filepath.Join(oramaDir, "data/namespaces/anchat/configs/sfu-node-1.yaml"),
		filepath.Join(oramaDir, "data/namespaces/other/configs/sfu-node-2.yaml"),
	}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("plan covers %q, want %q", paths, want)
	}
}

// A node with no SFU (every fresh install, most tenants) has nothing to hand
// over and needs no account looked up.
func TestServiceOwnershipPlan_noSFUConfigsIsAnEmptyPlan(t *testing.T) {
	oramaDir := filepath.Join(t.TempDir(), ".orama")
	writeTree(t, oramaDir, "data/namespaces/anchat/configs/gateway-node-1.yaml")
	lookedUp := false
	ids := accountIDs{
		uid: func(string) (int, error) { lookedUp = true; return 0, nil },
		gid: func(string) (int, error) { lookedUp = true; return 0, nil },
	}
	changes, err := serviceOwnershipPlan(oramaDir, ids)
	if err != nil || len(changes) != 0 {
		t.Fatalf("plan = %v, %v; want empty", changes, err)
	}
	if lookedUp {
		t.Error("an empty plan looked accounts up")
	}
}

// A missing orama-sfu group means the accounts step did not run; the plan
// fails rather than leaving the config in a group the SFU cannot read.
func TestServiceOwnershipPlan_missingSFUGroupIsAnError(t *testing.T) {
	oramaDir := filepath.Join(t.TempDir(), ".orama")
	writeTree(t, oramaDir, "data/namespaces/anchat/configs/sfu-node-1.yaml")
	ids := accountIDs{uid: fakeIDs.uid, gid: func(name string) (int, error) { return 0, errors.New("no group " + name) }}
	_, err := serviceOwnershipPlan(oramaDir, ids)
	if err == nil || !strings.Contains(err.Error(), "orama-sfu") {
		t.Fatalf("err = %v, want one naming orama-sfu", err)
	}
}

type recordingSetter struct {
	calls   []string
	failOn  string
	failErr error
}

func (r *recordingSetter) Chown(path string, uid, gid int) error {
	r.calls = append(r.calls, "chown "+filepath.Base(path))
	if r.failOn == "chown" {
		return r.failErr
	}
	return nil
}

func (r *recordingSetter) Chmod(path string, mode fs.FileMode) error {
	r.calls = append(r.calls, "chmod "+filepath.Base(path))
	if r.failOn == "chmod" {
		return r.failErr
	}
	return nil
}

func TestApplyOwnership_ownerBeforeModeForEachFile(t *testing.T) {
	r := &recordingSetter{}
	changes := []ownershipChange{{path: "/a/sfu-1.yaml", mode: 0o640}, {path: "/b/sfu-2.yaml", mode: 0o640}}
	if err := applyOwnership(r, changes); err != nil {
		t.Fatal(err)
	}
	want := []string{"chown sfu-1.yaml", "chmod sfu-1.yaml", "chown sfu-2.yaml", "chmod sfu-2.yaml"}
	if !reflect.DeepEqual(r.calls, want) {
		t.Fatalf("calls %q, want %q", r.calls, want)
	}
}

func TestApplyOwnership_stopsAtTheFirstFailure(t *testing.T) {
	r := &recordingSetter{failOn: "chown", failErr: errors.New("operation not permitted")}
	changes := []ownershipChange{{path: "/a/sfu-1.yaml"}, {path: "/b/sfu-2.yaml"}}
	err := applyOwnership(r, changes)
	if err == nil || !strings.Contains(err.Error(), "sfu-1.yaml") {
		t.Fatalf("err = %v, want one naming sfu-1.yaml", err)
	}
	if len(r.calls) != 1 {
		t.Fatalf("carried on after the failure: %q", r.calls)
	}
}

// The orama user owns the tree and can plant a symlink where an SFU config
// is; root must refuse it, not chown whatever it points at.
func TestApplyOwnership_refusesASymlinkThroughRootfs(t *testing.T) {
	home := t.TempDir()
	oramaDir := filepath.Join(home, ".orama")
	writeTree(t, oramaDir, "secrets/cluster-secret")
	link := filepath.Join(oramaDir, "data/namespaces/anchat/configs/sfu-node-1.yaml")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(oramaDir, "secrets/cluster-secret"), link); err != nil {
		t.Fatal(err)
	}
	changes := []ownershipChange{{path: link, uid: os.Getuid(), gid: os.Getgid(), mode: 0o640}}
	err := applyOwnership(OramaRoot(oramaDir), changes)
	if !errors.Is(err, rootfs.ErrSymlink) {
		t.Fatalf("err = %v, want rootfs.ErrSymlink", err)
	}
	info, err := os.Stat(filepath.Join(oramaDir, "secrets/cluster-secret"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("the symlink's target was changed to %04o", info.Mode().Perm())
	}
}

func TestApplyOwnership_setsTheModeThroughRootfs(t *testing.T) {
	oramaDir := filepath.Join(t.TempDir(), ".orama")
	writeTree(t, oramaDir, "data/namespaces/anchat/configs/sfu-node-1.yaml")
	path := filepath.Join(oramaDir, "data/namespaces/anchat/configs/sfu-node-1.yaml")
	changes := []ownershipChange{{path: path, uid: os.Getuid(), gid: os.Getgid(), mode: 0o640}}
	if err := applyOwnership(OramaRoot(oramaDir), changes); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Errorf("mode %04o, want 0640", info.Mode().Perm())
	}
}
