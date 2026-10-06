package main

import (
	"fmt"
	"strings"

	oramaversion "github.com/DeBrosOfficial/network/pkg/version"
)

// buildInfo is what the version command reports.
type buildInfo struct {
	Version string
	Commit  string
	Date    string
	// Dirty means the working tree had uncommitted changes at build time.
	Dirty bool
}

// resolveBuildInfo determines what this binary actually is.
//
// The version is compiled in (see pkg/version), so it is right in every build
// path. A -ldflags value still wins, because that is how a release build stamps
// the tag it is releasing, which is more specific than the file in the tree.
//
// The commit and date come only from -ldflags. They are deliberately not read
// back from Go's VCS stamp: inside a git worktree that stamp records the main
// repository's HEAD and reports the tree as unmodified, so it would name a
// commit this binary was not built from.
func resolveBuildInfo(ldVersion, ldCommit, ldDate string) buildInfo {
	info := buildInfo{Version: ldVersion, Commit: ldCommit, Date: ldDate}
	if info.Version == "" || info.Version == "dev" {
		info.Version = oramaversion.Current
	}
	return info
}

// String renders the version line.
func (b buildInfo) String() string {
	var line strings.Builder
	line.WriteString("orama ")
	if b.Version == "" {
		line.WriteString("unknown")
	} else {
		line.WriteString(b.Version)
	}
	if b.Commit != "" {
		fmt.Fprintf(&line, " (commit %s", b.Commit)
		if b.Dirty {
			// A dirty build does not correspond to any commit, and comparing
			// it against a node's version would say they match when they do
			// not.
			line.WriteString(", modified")
		}
		line.WriteString(")")
	}
	if b.Date != "" {
		fmt.Fprintf(&line, " built %s", b.Date)
	}
	return line.String()
}
