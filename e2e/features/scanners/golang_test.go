//go:build e2e_fleet

package scanners

import (
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/realistic"
)

// Exit codes the Go scanners document.
const (
	// govulncheckAffected: "Your code is affected by N vulnerabilities"
	// (golang.org/x/vuln/cmd/govulncheck, text mode).
	govulncheckAffected = 3
	// staticcheckFindings and gosecFindings: problems were reported.
	staticcheckFindings = 1
	gosecFindings       = 1
)

// tagArgs is the -tags flag for m, or nothing.
func tagArgs(m module) []string {
	if m.tags == "" {
		return nil
	}
	return []string{"-tags", m.tags}
}

// TestGovulncheck_modulesUnaffected: no module calls a function with a known
// vulnerability (govulncheck reports only reachable ones).
func TestGovulncheck_modulesUnaffected(t *testing.T) {
	t.Parallel()
	realistic.Tool(t, "govulncheck", "install golang.org/x/vuln/cmd/govulncheck to scan for known vulnerabilities")
	s := newScan(t)
	for _, m := range goModules {
		t.Run(m.dir, func(t *testing.T) {
			t.Parallel()
			res := s.run(t, m.dir, vulnBudget, "govulncheck", append(tagArgs(m), "./...")...)
			switch res.Exit {
			case 0:
			case govulncheckAffected:
				t.Errorf("%s calls vulnerable code:\n%s", m.dir, realistic.Tail(res.Stdout))
			default:
				t.Fatalf("govulncheck could not scan %s (exit %d):\n%s", m.dir, res.Exit, realistic.Tail(res.Output()))
			}
		})
	}
}

// TestStaticcheck_modulesClean: staticcheck reports nothing in any module.
func TestStaticcheck_modulesClean(t *testing.T) {
	t.Parallel()
	realistic.Tool(t, "staticcheck", "install honnef.co/go/tools/cmd/staticcheck to lint the modules")
	s := newScan(t)
	for _, m := range goModules {
		t.Run(m.dir, func(t *testing.T) {
			t.Parallel()
			res := s.run(t, m.dir, staticBudget, "staticcheck", append(tagArgs(m), "./...")...)
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
