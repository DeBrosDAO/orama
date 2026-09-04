// Package version reports what this build of Orama is.
//
// The version used to exist only as a -ldflags value, so it was correct when
// built through `make build` or GoReleaser and the string "dev" everywhere
// else — `go build`, `go install`, `go run`, an IDE. NODE_REPLACEMENT.md makes
// matching the CLI's version against the node's a mandatory gate before a
// rolling upgrade, and a binary that cannot say what it is makes that gate
// unenforceable.
//
// Reading it back from the VCS stamp is not a fix: inside a git worktree Go
// records the *main* repository's HEAD and reports the tree as unmodified, so
// the answer is confidently wrong exactly where an operator would rely on it.
//
// So the version is compiled in. version.txt is embedded, a test asserts it
// equals the repository's VERSION file, and `make bump` writes both.
package version

import (
	_ "embed"
	"strings"
)

//go:embed version.txt
var embedded string

// Current is the version this binary was built from.
//
// It is a var so a release build can still override it with -ldflags, which is
// what GoReleaser does with the tag it is releasing. The embedded value is what
// every other build path gets.
var Current = strings.TrimSpace(embedded)
