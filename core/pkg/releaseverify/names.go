package releaseverify

import (
	"encoding/json"
	"fmt"
	"regexp"
)

// A release channel is not a TUF role of its own. The RootWallet agent signs
// only the four top-level roles, so every release is a target of the
// top-level targets role, and a channel is the path prefix of its targets:
//
//	nightly/orama-0.3.1-linux-amd64.tar.gz
//	main/orama-0.3.0-linux-arm64.tar.gz
//	dev/my-branch/orama-0.3.1-linux-amd64.tar.gz
//
// A client reads the targets under its channel's prefix and takes the newest
// version for its architecture.
const (
	// maxChannelSegmentLen bounds one segment of a channel name.
	maxChannelSegmentLen = 32
	// archiveFilePrefix opens the file name `orama build` gives an archive
	// (build.ArchiveName).
	archiveFilePrefix = "orama-"
)

// channelPattern is a channel: "nightly", "main", or "dev/<branch>". Each
// segment is 1 to 32 characters of a-z, 0-9 and "-". A name becomes part of a
// target path in the repository and a URL on its host, so it carries no dot,
// upper case or other separator.
var channelPattern = regexp.MustCompile(`^[a-z0-9-]{1,32}(/[a-z0-9-]{1,32})?$`)

// archiveTargetPattern is the target name of a release archive:
// "<channel>/orama-<version>-linux-<arch>.tar.gz".
var archiveTargetPattern = regexp.MustCompile(`^([a-z0-9-]{1,32}(?:/[a-z0-9-]{1,32})?)/orama-([0-9][0-9A-Za-z.+-]*)-linux-(amd64|arm64)\.tar\.gz$`)

// ValidChannel accepts a release channel name (see channelPattern). It is the
// one place a channel is judged: where the update policy is written, where a
// release is cut and where one is read.
func ValidChannel(name string) error {
	if !channelPattern.MatchString(name) {
		return fmt.Errorf("release channel %q must be one or two segments of 1 to %d characters from a-z, 0-9 and -, separated by / (nightly, main, dev/<branch>)",
			name, maxChannelSegmentLen)
	}
	return nil
}

// ArchiveRef is what a release archive's target name says.
type ArchiveRef struct {
	Channel string
	Version string
	Arch    string
}

// ArchiveCustom is the custom field a published archive target carries, so the
// version, architecture and channel are readable from the signed targets bytes
// (and shown to the person approving the signature) without parsing a file
// name. A client holds it to the name: a target whose two disagree is not a
// candidate.
type ArchiveCustom struct {
	Version string `json:"version"`
	Arch    string `json:"arch"`
	Channel string `json:"channel"`
}

// ArchiveTarget is the target name of an archive.
func ArchiveTarget(channel, version, arch string) string {
	return fmt.Sprintf("%s/%s%s-linux-%s.tar.gz", channel, archiveFilePrefix, version, arch)
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

// customMatches reports whether a target's custom field agrees with its name.
// A target with no custom field is judged by its name alone.
func customMatches(custom json.RawMessage, ref ArchiveRef) bool {
	if custom == nil {
		return true
	}
	var c ArchiveCustom
	if err := json.Unmarshal(custom, &c); err != nil {
		return false
	}
	return c == ArchiveCustom{Version: ref.Version, Arch: ref.Arch, Channel: ref.Channel}
}
