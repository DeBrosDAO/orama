//go:build e2e_fleet

package clisandboxbuild

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
)

// deprecatedSign is the notice cobra prints for build's hidden --sign flag
// (cmd/buildcmd/build.go MarkDeprecated).
const deprecatedSign = "--sign has been deprecated"

// TestBuild_outsideCheckoutBuildsNothing: build cross-compiles from the
// checkout it is run in; run anywhere else it is refused naming the project
// root and writes no archive.
func TestBuild_outsideCheckoutBuildsNothing(t *testing.T) {
	t.Parallel()
	cli := bareHome(t)
	out := filepath.Join(cli.Home, "archive.tar.gz")
	infra.ExpectRefused(t, infra.Run(t, cli, "build", "--unsigned", "--output", out), "project root")
	if _, err := os.Stat(out); err == nil {
		t.Errorf("a refused build wrote %s", out)
	}
}

// TestBuild_badArchIsUsage: an architecture build does not support is a bad
// value on the command line (exit 2, clierr CodeUsage), refused before the
// build looks for anything.
func TestBuild_badArchIsUsage(t *testing.T) {
	t.Parallel()
	res := infra.Run(t, bareHome(t), "build", "--unsigned", "--arch", "sparc")
	infra.ExpectExit(t, res, infra.ExitUsage, "arch")
}

// TestBuild_deprecatedSignFlagSaysSo: --sign is still accepted so old scripts
// run, and says signing is now the default.
func TestBuild_deprecatedSignFlagSaysSo(t *testing.T) {
	t.Parallel()
	res := infra.Run(t, bareHome(t), "build", "--sign", "--unsigned")
	infra.ExpectRefused(t, res, deprecatedSign)
}
