package build

import (
	"strconv"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
)

// Two builds of one release must produce the same archive, byte for byte, so
// that the people who sign a release can each rebuild it and compare hashes
// before they sign (docs/DEV_DEPLOY.md, "Reproducible builds"). What would
// differ between two builds is fixed here: the build date, the Go flags that
// leak paths and VCS state into a binary, the environment variables that
// switch off module checksum verification, and the tar and gzip headers.

// sourceDateEpochVar is the reproducible-builds.org convention for the time a
// build stamps into what it makes.
const sourceDateEpochVar = "SOURCE_DATE_EPOCH"

// dateLayout is how the build date is written in the manifest and the
// binaries' version strings. archivetrust parses it as RFC 3339.
const dateLayout = "2006-01-02T15:04:05Z"

// buildDate is the time the build is stamped with: SOURCE_DATE_EPOCH when it
// is set (a release build sets it to the commit's time), else now. A value
// that is set but is not a count of seconds fails the build, since a typo
// would otherwise quietly produce an archive that cannot be reproduced.
func buildDate(environ []string, now time.Time) (time.Time, error) {
	for _, entry := range environ {
		value, ok := strings.CutPrefix(entry, sourceDateEpochVar+"=")
		if !ok {
			continue
		}
		secs, err := strconv.ParseInt(value, 10, 64)
		if err != nil || secs < 0 {
			return time.Time{}, clierr.Usage("%s=%q is not a count of seconds since the Unix epoch", sourceDateEpochVar, value)
		}
		return time.Unix(secs, 0).UTC(), nil
	}
	return now.UTC().Truncate(time.Second), nil
}

// unpinnedGoEnv are the variables through which an environment turns off the
// Go toolchain's verification of what it downloads or changes what it builds:
// module checksum lookups, private-module and insecure exemptions, and default
// flags such as -mod=mod. A build ignores them, so a release does not depend
// on the machine that happened to build it.
var unpinnedGoEnv = []string{
	"GOFLAGS", "GOSUMDB", "GONOSUMDB", "GONOSUMCHECK", "GOPRIVATE", "GONOPROXY", "GOINSECURE",
}

// hermeticGoEnv returns environ without the variables in unpinnedGoEnv.
func hermeticGoEnv(environ []string) []string {
	out := make([]string, 0, len(environ))
next:
	for _, entry := range environ {
		for _, name := range unpinnedGoEnv {
			if strings.HasPrefix(entry, name+"=") {
				continue next
			}
		}
		out = append(out, entry)
	}
	return out
}

// goLDFlags are the linker flags every Go binary in the archive is built
// with: stripped, and with no build id (the id is derived from build inputs
// that include the temporary directory).
const goLDFlags = "-s -w -buildid="

// goReproducibleArgs are the `go build` flags that keep the build machine's
// paths and the checkout's VCS state out of a binary, and that make the
// toolchain refuse a dependency the checked-in go.sum does not list.
var goReproducibleArgs = []string{"-mod=readonly", "-trimpath", "-buildvcs=false"}

// goBuildCommandArgs is `go build` with goReproducibleArgs, the linker flags
// and the output path, followed by the packages.
func goBuildCommandArgs(ldflags, output string, packages ...string) []string {
	args := append([]string{"build"}, goReproducibleArgs...)
	args = append(args, "-ldflags", ldflags, "-o", output)
	return append(args, packages...)
}
