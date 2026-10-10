//go:build e2e_fleet

package scanners

import (
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/cliconf"
	"github.com/DeBrosOfficial/network/e2e/features/internal/realistic"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// feature names this package's artifact directory.
const feature = "scanners"

// Budgets per tool run. The stage gives a package 90 minutes and the tests
// run in parallel, so each bound is generous but below that.
const (
	vulnBudget   = 15 * time.Minute
	staticBudget = 20 * time.Minute
	auditBudget  = 5 * time.Minute
	secretBudget = 15 * time.Minute
	fuzzBudget   = 5 * time.Minute
	raceBudget   = 45 * time.Minute
)

// module is one Go module of the repository and the build tags its files need.
type module struct {
	dir  string
	tags string
	// pkg is the package pattern govulncheck scans; "" means ./... . A module
	// that only pins a third-party program (a go.mod with a tool directive and
	// no packages of its own) names that program's main package.
	pkg string
}

// goModules are the repository's Go modules. The e2e module's feature files
// only build with the e2e_fleet tag.
var goModules = []module{{dir: "core"}, {dir: "chain"}, {dir: "e2e", tags: "e2e_fleet"}, {dir: "caddy"}}

// pinnedModules pin a third-party program that ships in the release archive
// (core/pkg/constants/versions.go names the versions). They hold no code of
// ours, so only govulncheck covers them, on the program's main package: the
// binary's dependencies are what an attacker reaches.
var pinnedModules = []module{
	{dir: "core/thirdparty/ipfs-cluster", pkg: "github.com/ipfs-cluster/ipfs-cluster/cmd/ipfs-cluster-service"},
	{dir: "core/thirdparty/olric", pkg: "github.com/olric-data/olric/cmd/olric-server"},
}

// vulnModules are every module govulncheck scans.
func vulnModules() []module {
	return append(append([]module{}, goModules...), pinnedModules...)
}

// scanPattern is the package pattern govulncheck scans in m.
func (m module) scanPattern() string {
	if m.pkg == "" {
		return "./..."
	}
	return m.pkg
}

// scan is what every scanner test starts from: the run (for the artifact dir
// and the evidence) and the checkout.
type scan struct {
	f    *fleet.Fleet
	root string
}

func newScan(t *testing.T) scan {
	t.Helper()
	f := harness.Fleet(t)
	return scan{f: f, root: cliconf.RepoRoot(t)}
}

// run runs a tool in dir (relative to the checkout) and keeps its whole
// output as an artifact named after the test.
func (s scan) run(t *testing.T, dir string, budget time.Duration, name string, args ...string) realistic.Local {
	t.Helper()
	res := realistic.RunLocal(t, s.f, filepath.Join(s.root, dir), budget, nil, name, args...)
	realistic.WriteText(t, s.f, feature, artifactName(t)+".txt", res.Cmd+"\nexit "+strconv.Itoa(res.Exit)+"\n\n"+res.Output())
	return res
}

// artifactName turns a (sub)test name into a file name.
func artifactName(t *testing.T) string {
	out := []rune(t.Name())
	for i, r := range out {
		if r == '/' || r == ' ' {
			out[i] = '_'
		}
	}
	return string(out)
}
