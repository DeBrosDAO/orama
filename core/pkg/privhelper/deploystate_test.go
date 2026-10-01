package privhelper

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func idle(string) (bool, error) { return false, nil }

func seedState(t *testing.T, root, instance string) {
	t.Helper()
	for _, rel := range []string{
		"var/lib/private/orama-deploy-" + instance + "/sub",
		"var/cache/private/orama-deploy-" + instance,
		"var/cache/private/orama-build/" + instance + "/deps",
	} {
		if err := os.MkdirAll(filepath.Join(root, rel), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "var/lib/private/orama-deploy-"+instance+"/sub/data"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("private/orama-deploy-"+instance, filepath.Join(root, "var/lib/orama-deploy-"+instance)); err != nil {
		t.Fatal(err)
	}
}

func TestPurgeDeployState_removesTheInstancesDirectoriesAndNoOthers(t *testing.T) {
	root := t.TempDir()
	seedState(t, root, "acme-web")
	seedState(t, root, "acme-web2")
	if err := PurgeDeployState(root, "acme-web", idle); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"var/lib/private/orama-deploy-acme-web", "var/lib/orama-deploy-acme-web",
		"var/cache/private/orama-deploy-acme-web", "var/cache/private/orama-build/acme-web"} {
		if _, err := os.Lstat(filepath.Join(root, rel)); !os.IsNotExist(err) {
			t.Errorf("%s survived: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "var/lib/private/orama-deploy-acme-web2/sub/data")); err != nil {
		t.Errorf("another instance's data went with it: %v", err)
	}
	if err := PurgeDeployState(root, "acme-web", idle); err != nil {
		t.Errorf("a second purge failed: %v", err)
	}
}

// A tenant's own symlink inside its state directory is unlinked, not followed.
func TestPurgeDeployState_doesNotFollowASymlinkInsideTheTree(t *testing.T) {
	root := t.TempDir()
	seedState(t, root, "acme-web")
	outside := filepath.Join(t.TempDir(), "keep")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "f"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "var/lib/private/orama-deploy-acme-web/escape")); err != nil {
		t.Fatal(err)
	}
	if err := PurgeDeployState(root, "acme-web", idle); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outside, "f")); err != nil {
		t.Errorf("the purge followed a symlink out of the state directory: %v", err)
	}
}

func TestPurgeDeployState_refusesAnInstanceThatIsAPath(t *testing.T) {
	root := t.TempDir()
	seedState(t, root, "acme-web")
	for _, instance := range []string{"", "../x", "a/b", "..", "."} {
		if err := PurgeDeployState(root, instance, idle); err == nil {
			t.Errorf("instance %q accepted", instance)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "var/lib/private/orama-deploy-acme-web")); err != nil {
		t.Errorf("a refused purge removed something: %v", err)
	}
}

func TestListDeployState_namesEveryInstanceWithADirectory(t *testing.T) {
	root := t.TempDir()
	got, err := ListDeployState(root)
	if err != nil || len(got) != 0 {
		t.Fatalf("an empty node listed %v, %v", got, err)
	}
	seedState(t, root, "acme-web")
	seedState(t, root, "beta-api")
	if err := os.MkdirAll(filepath.Join(root, "var/lib/private/other-service"), 0o700); err != nil {
		t.Fatal(err)
	}
	got, err = ListDeployState(root)
	if err != nil || !reflect.DeepEqual(got, []string{"acme-web", "beta-api"}) {
		t.Fatalf("listed %v, %v; want the two instances and not the other service", got, err)
	}
}

func TestValidate_purgeAndListState(t *testing.T) {
	for _, argv := range [][]string{{"deploy", "purge", "acme-web"}, {"deploy", "list-state"}} {
		if _, err := Validate(argv); err != nil {
			t.Errorf("%q refused: %v", argv, err)
		}
	}
	for _, argv := range [][]string{{"deploy", "purge"}, {"deploy", "purge", "../etc"}, {"deploy", "list-state", "x"}} {
		if _, err := Validate(argv); err == nil {
			t.Errorf("%q allowed", argv)
		}
	}
}

// The helper decides, not the gateway: a unit that is running keeps its
// directories, whichever of the instance's five units it is.
func TestPurgeDeployState_refusesWhileAUnitIsActive(t *testing.T) {
	for _, runtime := range []string{"node", "npm", "go", "build", "clean"} {
		root := t.TempDir()
		seedState(t, root, "acme-web")
		busy := DeployUnitName(runtime, "acme-web")
		err := PurgeDeployState(root, "acme-web", func(unit string) (bool, error) { return unit == busy, nil })
		if err == nil || !strings.Contains(err.Error(), busy) {
			t.Errorf("%s active: err %v, want a refusal naming the unit", runtime, err)
		}
		if _, err := os.Stat(filepath.Join(root, "var/lib/private/orama-deploy-acme-web")); err != nil {
			t.Errorf("%s active: the directories were removed: %v", runtime, err)
		}
	}
}

func TestPurgeDeployState_refusesWhenTheUnitStateIsUnknown(t *testing.T) {
	root := t.TempDir()
	seedState(t, root, "acme-web")
	err := PurgeDeployState(root, "acme-web", func(string) (bool, error) { return false, errors.New("systemctl unavailable") })
	if err == nil {
		t.Fatal("a purge went ahead without knowing whether the unit runs")
	}
	if _, err := os.Stat(filepath.Join(root, "var/lib/private/orama-deploy-acme-web")); err != nil {
		t.Errorf("the directories were removed: %v", err)
	}
}
