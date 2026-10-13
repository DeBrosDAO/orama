package setup

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/archivetrust"
)

// requireGNUTools skips the test unless the machine has the tools a node has: GNU
// tar and GNU find (the repack uses their options), awk, gzip and install.
func requireGNUTools(t *testing.T) string {
	t.Helper()
	sh, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash to run the script with")
	}
	for tool, banner := range map[string]string{"tar": "GNU tar", "find": "GNU findutils"} {
		out, err := exec.Command(tool, "--version").CombinedOutput()
		if err != nil || !strings.Contains(string(out), banner) {
			t.Skipf("%s here is not %s, which the repack runs on a node", tool, banner)
		}
	}
	for _, tool := range []string{"awk", "gzip", "install"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("no %s", tool)
		}
	}
	return sh
}

// member is one entry of a fixture archive.
type member struct {
	name, body, link string
	typeflag         byte
	mode             int64
}

// releaseMembers are what an unsigned release has: directories, files, a dotfile in
// bin/ and the manifest last, as `orama maint build` writes them.
func releaseMembers() []member {
	return []member{
		{name: "bin/", typeflag: tar.TypeDir, mode: 0o755},
		{name: "bin/orama", body: "cli", typeflag: tar.TypeReg, mode: 0o755},
		{name: "bin/.keep", body: "keep", typeflag: tar.TypeReg, mode: 0o644},
		{name: "systemd/", typeflag: tar.TypeDir, mode: 0o755},
		{name: "systemd/x.service", body: "[Unit]\n", typeflag: tar.TypeReg, mode: 0o644},
		{name: "manifest.json", body: "the release's own manifest", typeflag: tar.TypeReg, mode: 0o644},
	}
}

func writeTarGz(t *testing.T, path string, members []member) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, m := range members {
		hdr := &tar.Header{Name: m.name, Typeflag: m.typeflag, Mode: m.mode, Size: int64(len(m.body)), Linkname: m.link}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(tw, m.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := errors.Join(tw.Close(), gz.Close(), f.Close()); err != nil {
		t.Fatal(err)
	}
}

// readTarGz is the names of an archive, in order, and the content of its files.
func readTarGz(t *testing.T, path string) (names []string, bodies map[string]string) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	bodies = map[string]string{}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return names, bodies
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, hdr.Name)
		if hdr.Typeflag == tar.TypeReg {
			b, err := io.ReadAll(tr)
			if err != nil {
				t.Fatal(err)
			}
			bodies[hdr.Name] = string(b)
		}
	}
}

// runAssemble runs the real repack script in a directory holding the download and
// the signed files.
func runAssemble(t *testing.T, sh string, members []member) (dir string, out []byte, err error) {
	t.Helper()
	dir = t.TempDir()
	writeTarGz(t, filepath.Join(dir, downloadedName), members)
	for name, body := range map[string]string{endorsedManifestName: "the signed manifest", endorsedSignatureName: "0xsignature"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	out, err = exec.Command(sh, "-c", assembleEndorsedScript(dir)).CombinedOutput()
	return dir, out, err
}

func TestAssembleEndorsedScript_theRepackedArchiveHasTheOriginalMembersAndIsAcceptedByTheStage(t *testing.T) {
	sh := requireGNUTools(t)
	dir, out, err := runAssemble(t, sh, releaseMembers())
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	names, bodies := readTarGz(t, filepath.Join(dir, madeArchiveName))
	for _, name := range names {
		if strings.HasPrefix(name, "./") || strings.HasPrefix(name, "/") {
			t.Errorf("member %q: the stage extracts bin/orama by that name, so no entry may begin with ./", name)
		}
	}
	want := []string{"bin", "bin/.keep", "bin/orama", "manifest.json", "manifest.sig", "systemd", "systemd/x.service"}
	var got []string
	for _, name := range names {
		got = append(got, strings.TrimSuffix(name, "/"))
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("members %v, want the original ones plus manifest.sig: %v", got, want)
	}
	if bodies["manifest.json"] != "the signed manifest" || bodies["manifest.sig"] != "0xsignature" {
		t.Errorf("manifest.json %q, manifest.sig %q: the signed files are not in the archive", bodies["manifest.json"], bodies["manifest.sig"])
	}
	for name, body := range map[string]string{"bin/orama": "cli", "bin/.keep": "keep", "systemd/x.service": "[Unit]\n"} {
		if bodies[name] != body {
			t.Errorf("%s = %q, want the original %q", name, bodies[name], body)
		}
	}
	// The layout the stage accepts: the same extraction `node stage-archive` runs.
	if err := archivetrust.Extract(filepath.Join(dir, madeArchiveName), t.TempDir()); err != nil {
		t.Errorf("the stage refuses the repacked archive: %v", err)
	}
	for _, left := range []string{"tree", downloadedName, endorsedManifestName, endorsedSignatureName} {
		if _, err := os.Stat(filepath.Join(dir, left)); err == nil {
			t.Errorf("%s was left in the archive directory", left)
		}
	}
}

func TestAssembleEndorsedScript_aMemberThatIsNotAFileOrADirectoryOrIsNamedOutsideTheTreeIsRefused(t *testing.T) {
	sh := requireGNUTools(t)
	bad := map[string]member{
		"a symlink":     {name: "bin/link", link: "/etc/passwd", typeflag: tar.TypeSymlink, mode: 0o777},
		"a hard link":   {name: "bin/hard", link: "bin/orama", typeflag: tar.TypeLink, mode: 0o644},
		"an absolute":   {name: "/etc/cron.d/x", body: "x", typeflag: tar.TypeReg, mode: 0o644},
		"a parent name": {name: "../escape", body: "x", typeflag: tar.TypeReg, mode: 0o644},
		"an inner ..":   {name: "bin/../../escape", body: "x", typeflag: tar.TypeReg, mode: 0o644},
	}
	for name, m := range bad {
		members := slices.Insert(releaseMembers(), 2, m)
		dir, out, err := runAssemble(t, sh, members)
		if err == nil {
			t.Errorf("%s was repacked", name)
			continue
		}
		if !strings.Contains(string(out), "the archive has a member") {
			t.Errorf("%s: the refusal does not say what was found:\n%s", name, out)
		}
		if _, statErr := os.Stat(filepath.Join(dir, "tree")); statErr == nil {
			t.Errorf("%s: the archive was unpacked before it was refused", name)
		}
		if _, statErr := os.Stat(filepath.Join(filepath.Dir(dir), "escape")); statErr == nil {
			t.Errorf("%s: a member was written outside the tree", name)
		}
	}
}
