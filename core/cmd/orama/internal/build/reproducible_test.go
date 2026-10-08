package build

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/archivetrust"
)

func TestBuildDate_sourceDateEpochWins(t *testing.T) {
	now := time.Date(2030, 1, 2, 3, 4, 5, 999, time.UTC)
	got, err := buildDate([]string{"PATH=/bin", "SOURCE_DATE_EPOCH=1700000000"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Unix(1700000000, 0).UTC(); !got.Equal(want) {
		t.Fatalf("build date = %s, want %s", got, want)
	}
}

func TestBuildDate_unsetIsNowToTheSecond(t *testing.T) {
	now := time.Date(2030, 1, 2, 3, 4, 5, 999, time.UTC)
	got, err := buildDate([]string{"PATH=/bin"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if want := now.Truncate(time.Second); !got.Equal(want) {
		t.Fatalf("build date = %s, want %s", got, want)
	}
}

func TestBuildDate_notASecondsCountIsRefused(t *testing.T) {
	for _, bad := range []string{"", "yesterday", "-1", "1.5", "0x10"} {
		_, err := buildDate([]string{"SOURCE_DATE_EPOCH=" + bad}, time.Now())
		if err == nil {
			t.Errorf("SOURCE_DATE_EPOCH=%q was accepted", bad)
			continue
		}
		var usage *clierr.Error
		if !errors.As(err, &usage) {
			t.Errorf("SOURCE_DATE_EPOCH=%q: %v is not a usage error", bad, err)
		}
	}
}

func TestHermeticGoEnv_dropsWhatSwitchesVerificationOff(t *testing.T) {
	in := []string{
		"PATH=/bin", "GOFLAGS=-mod=mod", "GONOSUMDB=*", "GOSUMDB=off", "GOPRIVATE=github.com/x",
		"GONOPROXY=x", "GOINSECURE=x", "GONOSUMCHECK=1", "GOPROXY=https://proxy.example", "GOCACHE=/c",
	}
	got := strings.Join(hermeticGoEnv(in), "\n")
	for _, kept := range []string{"PATH=/bin", "GOPROXY=https://proxy.example", "GOCACHE=/c"} {
		if !strings.Contains(got, kept) {
			t.Errorf("%s was dropped:\n%s", kept, got)
		}
	}
	for _, name := range unpinnedGoEnv {
		if strings.Contains(got, name+"=") {
			t.Errorf("%s survived:\n%s", name, got)
		}
	}
	for _, pinned := range pinnedGoEnv {
		if !strings.Contains(got, pinned) {
			t.Errorf("%s is not set:\n%s", pinned, got)
		}
	}
	again := strings.Join(hermeticGoEnv([]string{"GOENV=/home/u/.config/go/env", "GOWORK=/src/go.work"}), "\n")
	if strings.Contains(again, "/home/u") || strings.Contains(again, "/src/go.work") {
		t.Errorf("the environment's GOENV and GOWORK were kept:\n%s", again)
	}
}

func TestGoBuildCommandArgs_readOnlyTrimmedAndWithoutVCS(t *testing.T) {
	args := goBuildCommandArgs(goLDFlags, "/out/x", "./cmd/x")
	joined := strings.Join(args, " ")
	for _, want := range []string{"-mod=readonly", "-trimpath", "-buildvcs=false", "-buildid=", "-o /out/x", "./cmd/x"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args %q missing %q", joined, want)
		}
	}
	if args[0] != "build" {
		t.Errorf("first argument is %q, want build", args[0])
	}
}

// archiveFixture lays out a build directory the way Build does, with the
// given modification time and file modes applied by the caller's umask.
func archiveFixture(t *testing.T, modTime time.Time, binMode, dataMode os.FileMode) *Builder {
	t.Helper()
	tmp := t.TempDir()
	for name, mode := range map[string]os.FileMode{
		"bin/orama":                  binMode,
		"bin/orama-node":             binMode,
		"systemd/orama-turn.service": dataMode,
	} {
		path := filepath.Join(tmp, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("content of "+name), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, modTime, modTime); err != nil {
			t.Fatal(err)
		}
	}
	return &Builder{tmpDir: tmp, binDir: filepath.Join(tmp, "bin"), date: "2026-10-01T00:00:00Z"}
}

func archiveBytes(t *testing.T, b *Builder, signature string) []byte {
	t.Helper()
	out := filepath.Join(t.TempDir(), "orama.tar.gz")
	m := &Manifest{Version: "1.2.3", Commit: "abc1234", Date: b.date, Arch: "amd64", Checksums: map[string]string{"orama": "00"}}
	if err := b.createArchive(out, m, []byte(`{"version":"1.2.3"}`), signature); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestCreateArchive_sameInputsGiveTheSameBytes(t *testing.T) {
	first := archiveBytes(t, archiveFixture(t, time.Unix(1_600_000_000, 0), 0o755, 0o644), "")
	second := archiveBytes(t, archiveFixture(t, time.Unix(1_700_000_000, 0), 0o775, 0o600), "")
	if sha256.Sum256(first) != sha256.Sum256(second) {
		t.Fatal("two builds of the same files differ: file times and the build machine's modes leak into the archive")
	}
}

func TestCreateArchive_entriesAreInFixedOrderOwnedByRootAtTheBuildDate(t *testing.T) {
	b := archiveFixture(t, time.Unix(1_600_000_000, 0), 0o750, 0o640)
	data := archiveBytes(t, b, "0xsig")
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if gz.Name != "" || !gz.ModTime.IsZero() {
		t.Errorf("gzip header carries a name %q or a time %s", gz.Name, gz.ModTime)
	}
	built, err := time.Parse(dateLayout, b.date)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, hdr.Name)
		if hdr.Uid != 0 || hdr.Gid != 0 || hdr.Uname != "" || hdr.Gname != "" {
			t.Errorf("%s is owned by %d:%d %q:%q, want root with no names", hdr.Name, hdr.Uid, hdr.Gid, hdr.Uname, hdr.Gname)
		}
		if !hdr.ModTime.Equal(built) {
			t.Errorf("%s has time %s, want the build date %s", hdr.Name, hdr.ModTime, built)
		}
		wantMode := int64(archiveFileMode)
		switch {
		case hdr.Typeflag == tar.TypeDir:
			wantMode = archiveDirMode
		case strings.HasPrefix(hdr.Name, "bin/"):
			wantMode = archiveExecMode
		}
		if hdr.Mode != wantMode {
			t.Errorf("%s has mode %o, want %o", hdr.Name, hdr.Mode, wantMode)
		}
	}
	want := []string{
		"bin/", "bin/orama", "bin/orama-node", "systemd/", "systemd/orama-turn.service",
		archivetrust.ManifestName, archivetrust.SignatureName,
	}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("entries = %v, want %v", names, want)
	}
}

func TestCreateArchive_aBadBuildDateIsRefused(t *testing.T) {
	b := archiveFixture(t, time.Now(), 0o755, 0o644)
	b.date = "yesterday"
	out := filepath.Join(t.TempDir(), "x.tar.gz")
	if err := b.createArchive(out, &Manifest{}, []byte("{}"), ""); err == nil {
		t.Fatal("an archive was written with a build date that is not a time")
	}
}
