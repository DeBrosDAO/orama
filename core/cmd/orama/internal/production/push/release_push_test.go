package push

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

// decodeScript returns the script a "printf <b64> | base64 -d | [sudo ]bash -s"
// command carries.
func decodeScript(t *testing.T, cmd string) string {
	t.Helper()
	fields := strings.Fields(cmd)
	if len(fields) < 3 || fields[0] != "printf" {
		t.Fatalf("not a printf pipeline: %q", cmd)
	}
	raw, err := base64.StdEncoding.DecodeString(fields[2])
	if err != nil {
		t.Fatalf("the command's payload is not base64: %v", err)
	}
	return string(raw)
}

func TestReleaseStageCommand_adoptsTheRootWhenMissingRotatesWhenDifferentThenStagesOnTheReleaseRoot(t *testing.T) {
	script := decodeScript(t, releaseStageCommand("sudo ", "/tmp/orama-push.AbCd1234", "nightly/orama-0.4.0-linux-amd64.tar.gz"))

	wantLines := []string{
		"set -eu",
		"[ -f /etc/orama/release-root.json ] || /usr/local/bin/orama node trust add-root /tmp/orama-push.AbCd1234/root.json",
		"cmp -s /tmp/orama-push.AbCd1234/root.json /etc/orama/release-root.json || /usr/local/bin/orama node trust add-root --rotate /tmp/orama-push.AbCd1234/root.json",
		"/usr/local/bin/orama node stage-archive --archive /tmp/orama-push.AbCd1234/archive.tar.gz" +
			" --release-metadata /tmp/orama-push.AbCd1234/meta --release-target nightly/orama-0.4.0-linux-amd64.tar.gz --release-only",
	}
	if got := strings.Split(strings.TrimSpace(script), "\n"); !slices.Equal(got, wantLines) {
		t.Errorf("script lines:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(wantLines, "\n"))
	}
}

func TestReleaseStageCommand_runsAsRootThroughSudoForANonRootLogin(t *testing.T) {
	if cmd := releaseStageCommand("sudo ", "/tmp/orama-push.AbCd1234", "nightly/a.tar.gz"); !strings.HasSuffix(cmd, "| sudo bash -s") {
		t.Errorf("command = %q, want it piped to sudo bash -s", cmd)
	}
	if cmd := releaseStageCommand("", "/tmp/orama-push.AbCd1234", "nightly/a.tar.gz"); !strings.HasSuffix(cmd, "| bash -s") {
		t.Errorf("command = %q, want it piped to bash -s", cmd)
	}
}

func TestReleaseToNode_refusesATargetThatCouldInjectShell(t *testing.T) {
	for _, target := range []string{"", "nightly/a.tar.gz; rm -rf /", "$(id)", "a b", "../x", "-x"} {
		_, err := ReleaseToNode(inspector.Node{Host: "h"}, ReleaseFiles{Target: target, MetadataDir: t.TempDir()})
		if err == nil || !strings.Contains(err.Error(), "not a name in the signed targets") {
			t.Errorf("target %q: err = %v, want a refusal before any SSH", target, err)
		}
	}
}

func writeFiles(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, n := range names {
		p := filepath.Join(dir, filepath.FromSlash(n))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMetadataFiles_listsEveryFileRelativelyIncludingSubdirectories(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, "timestamp.json", "snapshot.json", "targets.json", "channels/nightly.json")

	got, err := metadataFiles(dir)
	if err != nil {
		t.Fatalf("metadataFiles: %v", err)
	}

	slices.Sort(got)
	if want := []string{"channels/nightly.json", "snapshot.json", "targets.json", "timestamp.json"}; !slices.Equal(got, want) {
		t.Errorf("files = %v, want %v", got, want)
	}
}

func TestMetadataFiles_refusesAnEmptyDirectory(t *testing.T) {
	if _, err := metadataFiles(t.TempDir()); err == nil || !strings.Contains(err.Error(), "0 metadata files") {
		t.Fatalf("err = %v, want the empty metadata refused", err)
	}
}

func TestMetadataFiles_refusesANameThatCannotGoInACommand(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, "a b.json")

	if _, err := metadataFiles(dir); err == nil || !strings.Contains(err.Error(), "cannot go in a remote command") {
		t.Fatalf("err = %v, want the unsafe name refused", err)
	}
}

func TestMetadataFiles_refusesASymlink(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, "real.json")
	if err := os.Symlink(filepath.Join(dir, "real.json"), filepath.Join(dir, "link.json")); err != nil {
		t.Fatal(err)
	}

	if _, err := metadataFiles(dir); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("err = %v, want the symlink refused", err)
	}
}

func TestMetadataDirs_createsEachSubdirectoryOnce(t *testing.T) {
	got := metadataDirs("/tmp/orama-push.AbCd1234", []string{"timestamp.json", "channels/nightly.json", "channels/main.json"})

	slices.Sort(got)
	want := []string{"/tmp/orama-push.AbCd1234/meta", "/tmp/orama-push.AbCd1234/meta/channels"}
	if !slices.Equal(got, want) {
		t.Errorf("dirs = %v, want %v", got, want)
	}
}

func TestUploadRoot_refusesAnEmptyRoot(t *testing.T) {
	if err := uploadRoot(inspector.Node{Host: "h"}, "/tmp/orama-push.AbCd1234", nil); err == nil {
		t.Fatal("a release with no root must be refused before anything is uploaded")
	}
}
