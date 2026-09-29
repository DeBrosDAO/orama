//go:build e2e_fleet

package scanners

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/realistic"
)

const (
	// leaksFound is the exit code both scanners are told to use for findings.
	leaksFound = 3
	// maxExtractedFile bounds one file taken out of an archive to scan.
	maxExtractedFile = 512 << 20
)

// finding is one secret-scanner result (gitleaks' report format, which
// infisical scan, a gitleaks fork, shares). The secret itself is redacted.
type finding struct {
	RuleID    string `json:"RuleID"`
	File      string `json:"File"`
	StartLine int    `json:"StartLine"`
}

// secretScanner builds the command that scans dir and writes a JSON report.
type secretScanner func(dir, report string) (string, []string)

// pickSecretScanner prefers gitleaks, then infisical scan.
func pickSecretScanner(t *testing.T) secretScanner {
	t.Helper()
	if _, err := exec.LookPath("gitleaks"); err == nil {
		return func(dir, report string) (string, []string) {
			return "gitleaks", []string{"dir", dir, "--no-banner", "--redact", "--report-format", "json", "--report-path", report, "--exit-code", "3"}
		}
	}
	realistic.Tool(t, "infisical", "install gitleaks (or the infisical CLI, whose `scan` is the same engine) to scan for secrets")
	return func(dir, report string) (string, []string) {
		return "infisical", []string{"scan", "--no-git", "--source", dir, "--redact", "--report-format", "json", "--report-path", report, "--exit-code", "3", "--silent", "--telemetry=false"}
	}
}

// scanDir scans dir and fails the test with every finding's place.
func (s scan) scanDir(t *testing.T, scanner secretScanner, dir, what string) {
	t.Helper()
	report := realistic.ArtifactPath(t, s.f, feature, artifactName(t)+"-report.json")
	name, args := scanner(dir, report)
	res := realistic.RunLocal(t, s.f, dir, secretBudget, nil, name, args...)
	switch res.Exit {
	case 0:
		return
	case leaksFound:
	default:
		t.Fatalf("%s could not scan %s (exit %d):\n%s", name, what, res.Exit, realistic.Tail(res.Output()))
	}
	var found []finding
	if err := realistic.ReadJSON(report, &found); err != nil {
		t.Fatalf("%s found secrets in %s but its report is unreadable: %v", name, what, err)
	}
	places := make([]string, 0, len(found))
	for _, fd := range found {
		places = append(places, fmt.Sprintf("%s:%d (%s)", strings.TrimPrefix(fd.File, dir+"/"), fd.StartLine, fd.RuleID))
	}
	t.Errorf("%s found %d secret(s) in %s (values redacted; report %s):\n%s", name, len(found), what, report, strings.Join(places, "\n"))
}

// TestSecretScan_committedTree: nothing committed at HEAD looks like a
// secret. The tree is what `git archive` exports, so ignored local files
// (a developer's secrets/ directory) are not part of it.
func TestSecretScan_committedTree(t *testing.T) {
	t.Parallel()
	scanner := pickSecretScanner(t)
	realistic.Tool(t, "git", "the committed tree is exported with git archive")
	s := newScan(t)
	tmp := t.TempDir()
	tarPath := filepath.Join(tmp, "head.tar")
	if res := realistic.RunLocal(t, s.f, s.root, secretBudget, nil, "git", "archive", "--format=tar", "-o", tarPath, "HEAD"); res.Exit != 0 {
		t.Fatalf("git archive HEAD failed (exit %d): %s", res.Exit, res.Stderr)
	}
	tree := filepath.Join(tmp, "tree")
	if err := extract(tarPath, tree, false); err != nil {
		t.Fatal(err)
	}
	s.scanDir(t, scanner, tree, "the committed tree at HEAD")
}

// TestSecretScan_releaseArchive: the archive the run built and installed on
// the fleet carries no secret in any file.
func TestSecretScan_releaseArchive(t *testing.T) {
	t.Parallel()
	scanner := pickSecretScanner(t)
	s := newScan(t)
	if s.f.State.ArchivePath == "" {
		t.Fatal("the run state names no release archive (state.archive_path); the build step must record it")
	}
	tree := filepath.Join(t.TempDir(), "archive")
	if err := extract(s.f.State.ArchivePath, tree, true); err != nil {
		t.Fatal(err)
	}
	s.scanDir(t, scanner, tree, "the release archive "+filepath.Base(s.f.State.ArchivePath))
}

// extract unpacks the regular files and directories of a tar (gzipped when
// gz) into dest. An entry whose name would leave dest is refused.
func extract(path, dest string, gz bool) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("failed to open %s: %w", path, err)
	}
	defer f.Close()
	var r io.Reader = f
	if gz {
		zr, err := gzip.NewReader(f)
		if err != nil {
			return fmt.Errorf("%s is not gzip: %w", path, err)
		}
		defer zr.Close()
		r = zr
	}
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", path, err)
		}
		if err := extractEntry(tr, h, dest); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}
}

func extractEntry(tr *tar.Reader, h *tar.Header, dest string) error {
	if !filepath.IsLocal(h.Name) {
		return fmt.Errorf("entry %q leaves the extraction directory", h.Name)
	}
	target := filepath.Join(dest, h.Name)
	switch h.Typeflag {
	case tar.TypeDir:
		return os.MkdirAll(target, 0o700)
	case tar.TypeReg:
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return fmt.Errorf("failed to create the directory of %s: %w", h.Name, err)
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return fmt.Errorf("failed to create %s: %w", h.Name, err)
		}
		_, cerr := io.Copy(out, io.LimitReader(tr, maxExtractedFile))
		if err := errors.Join(cerr, out.Close()); err != nil {
			return fmt.Errorf("failed to extract %s: %w", h.Name, err)
		}
	}
	return nil
}
