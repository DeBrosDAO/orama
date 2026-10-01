//go:build e2e_fleet

package scanners

import (
	_ "embed"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/realistic"
	"github.com/DeBrosOfficial/network/e2e/harness/vulnaccept"
)

// acceptedYAML is the checked-in list of vulnerabilities the project accepts
// (package vulnaccept judges a scan against it).
//
//go:embed govulncheck-accepted.yaml
var acceptedYAML []byte

// Exit codes the Go scanners document.
const (
	// staticcheckFindings and gosecFindings: problems were reported.
	staticcheckFindings = 1
	gosecFindings       = 1
)

// staticcheckPackage is the staticcheck the modules are linted with, run
// through `go run` so the linter is built by the Go toolchain that builds the
// modules. A staticcheck installed on the runner is as old as its last
// install: 2025.1.1 cannot load Go 1.27 code and reported that as findings.
// v0.8.1 is staticcheck 2026.2.1, the first release after Go 1.27.
const staticcheckPackage = "honnef.co/go/tools/cmd/staticcheck@v0.8.1"

// govulncheckPackage is the govulncheck the modules are scanned with, run
// through `go run` for the same reason as staticcheck: an installed binary is
// built with the Go of its last install, and a govulncheck built with an older
// Go than a module's go directive refuses it ("package requires newer Go
// version"). v1.8.0 is the latest release.
const govulncheckPackage = "golang.org/x/vuln/cmd/govulncheck@v1.8.0"

// tagArgs is the -tags flag for m, or nothing.
func tagArgs(m module) []string {
	if m.tags == "" {
		return nil
	}
	return []string{"-tags", m.tags}
}

// TestGovulncheck_modulesUnaffected: no module calls a function with a known
// vulnerability (govulncheck reports only reachable ones) beyond those listed,
// with a reason and a review date, in govulncheck-accepted.yaml. A reachable
// vulnerability not listed, a listed one no longer reported, and a listed one
// past its review_by all fail. Network: govulncheck downloads the Go
// vulnerability database (vuln.go.dev) and, like go build, may fetch modules
// through GOPROXY, from the runner; it sends module paths and versions, never
// source.
func TestGovulncheck_modulesUnaffected(t *testing.T) {
	t.Parallel()
	realistic.Tool(t, "go", "the pinned govulncheck is built and run with `go run`")
	now := time.Now()
	accepted, err := vulnaccept.Parse(acceptedYAML, now)
	if err != nil {
		t.Fatal(err)
	}
	s := newScan(t)
	for _, m := range goModules {
		t.Run(m.dir, func(t *testing.T) {
			t.Parallel()
			args := append(append([]string{"run", govulncheckPackage, "-format", "json"}, tagArgs(m)...), "./...")
			res := s.run(t, m.dir, vulnBudget, "go", args...)
			if res.Exit != 0 {
				t.Fatalf("govulncheck could not scan %s (exit %d):\n%s", m.dir, res.Exit, realistic.Tail(res.Output()))
			}
			found, err := vulnaccept.Called([]byte(res.Stdout))
			if err != nil {
				t.Fatalf("%s: %v\n%s", m.dir, err, realistic.Tail(res.Output()))
			}
			for _, problem := range vulnaccept.Judge(m.dir, found, accepted, now) {
				t.Error(problem)
			}
		})
	}
}

// TestStaticcheck_modulesClean: staticcheck reports nothing in any module.
func TestStaticcheck_modulesClean(t *testing.T) {
	t.Parallel()
	realistic.Tool(t, "go", "the pinned staticcheck is built and run with `go run`")
	s := newScan(t)
	for _, m := range goModules {
		t.Run(m.dir, func(t *testing.T) {
			t.Parallel()
			res := s.run(t, m.dir, staticBudget, "go", append(append([]string{"run", staticcheckPackage}, tagArgs(m)...), "./...")...)
			switch res.Exit {
			case 0:
			case staticcheckFindings:
				t.Errorf("staticcheck reports problems in %s:\n%s", m.dir, realistic.Tail(res.Output()))
			default:
				t.Fatalf("staticcheck could not lint %s (exit %d):\n%s", m.dir, res.Exit, realistic.Tail(res.Output()))
			}
		})
	}
}

// TestGosec_noHighSeverityFindings: gosec finds nothing it rates high
// severity with high confidence. Lower ratings are recorded in the artifact
// and not gated: they are mostly the deliberate exec and file calls of an
// installer.
func TestGosec_noHighSeverityFindings(t *testing.T) {
	t.Parallel()
	realistic.Tool(t, "gosec", "install github.com/securego/gosec/v2/cmd/gosec to run the security linter")
	s := newScan(t)
	for _, m := range goModules {
		t.Run(m.dir, func(t *testing.T) {
			t.Parallel()
			args := []string{"-quiet", "-exclude-generated", "-severity", "high", "-confidence", "high"}
			if m.tags != "" {
				args = append(args, "-tags", m.tags)
			}
			res := s.run(t, m.dir, staticBudget, "gosec", append(args, "./...")...)
			switch res.Exit {
			case 0:
			case gosecFindings:
				t.Errorf("gosec reports high-severity findings in %s:\n%s", m.dir, realistic.Tail(res.Output()))
			default:
				t.Fatalf("gosec could not scan %s (exit %d):\n%s", m.dir, res.Exit, realistic.Tail(res.Output()))
			}
		})
	}
}
