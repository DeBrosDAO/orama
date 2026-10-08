package releaseverify

import (
	"fmt"
	"regexp"
)

// A release archive is a target named
// "<channel>/orama-<version>-linux-<arch>.tar.gz": the channel is the
// delegated role that signs it (delegate.go), and the rest is the file name
// `orama build` gives the archive (build.ArchiveName).
var archiveTargetPattern = regexp.MustCompile(`^([a-z0-9-]{1,32})/orama-([0-9][0-9A-Za-z.+-]*)-linux-(amd64|arm64)\.tar\.gz$`)

// ArchiveRef is what a release archive's target name says.
type ArchiveRef struct {
	Channel string
	Version string
	Arch    string
}

// ArchiveTarget is the target name of an archive.
func ArchiveTarget(channel, version, arch string) string {
	return fmt.Sprintf("%s/orama-%s-linux-%s.tar.gz", channel, version, arch)
}

// ParseArchiveTarget reads a target name, refusing one that is not a release
// archive's.
func ParseArchiveTarget(name string) (ArchiveRef, error) {
	m := archiveTargetPattern.FindStringSubmatch(name)
	if m == nil {
		return ArchiveRef{}, fmt.Errorf("%q is not a release archive name (<channel>/orama-<version>-linux-<arch>.tar.gz)", name)
	}
	return ArchiveRef{Channel: m[1], Version: m[2], Arch: m[3]}, nil
}
