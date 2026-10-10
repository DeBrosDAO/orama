package releasepub

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/releaseverify"
)

// Channel names a maintainer may cut: nightly (stagenet), main (testnet and
// later), and dev/<branch>.
const (
	ChannelNightly = "nightly"
	ChannelMain    = "main"
	devPrefix      = "dev/"
	// maxBranchSlug is the longest branch slug a dev channel carries
	// (releaseverify.ValidChannel's segment length).
	maxBranchSlug = 32
)

// notSlug matches what a branch name loses when it becomes a channel segment.
var notSlug = regexp.MustCompile(`[^a-z0-9]+`)

// Channel is a release channel a maintainer cuts into.
type Channel struct{ Name string }

// ParseChannel reads --channel: "nightly", "main", or "dev/<branch>". A branch
// name is lowercased and anything but letters and digits becomes a dash, so
// "dev/Feat/Join-One" is the channel dev/feat-join-one; two branches that slug
// alike share a channel.
func ParseChannel(s string) (Channel, error) {
	name := strings.TrimSpace(s)
	switch {
	case name == ChannelNightly || name == ChannelMain:
	case strings.HasPrefix(name, devPrefix):
		slug := strings.Trim(notSlug.ReplaceAllString(strings.ToLower(strings.TrimPrefix(name, devPrefix)), "-"), "-")
		if slug == "" || len(slug) > maxBranchSlug {
			return Channel{}, fmt.Errorf("branch %q makes a dev channel segment of %d characters; it must be 1 to %d letters, digits and dashes",
				strings.TrimPrefix(name, devPrefix), len(slug), maxBranchSlug)
		}
		name = devPrefix + slug
	default:
		return Channel{}, fmt.Errorf("channel %q is not nightly, main or dev/<branch>", s)
	}
	if err := releaseverify.ValidChannel(name); err != nil {
		return Channel{}, err
	}
	return Channel{Name: name}, nil
}

// TimestampValidity is how long a timestamp signed for this channel is valid:
// 7 days for nightly and dev, 30 for main. The timestamp is the repository's,
// not the channel's: the next cut or refresh on any channel replaces it.
func (c Channel) TimestampValidity() time.Duration {
	if c.Name == ChannelMain {
		return MainTimestampValidity
	}
	return ShortTimestampValidity
}

// Tag is the GitHub release the channel's version is uploaded to. "/" becomes
// "." (a channel segment has no dot), so dev/x and a channel dev-x never share
// a tag; the release host's redirect builds the same name from the path.
func (c Channel) Tag(version string) string {
	return "release-" + strings.ReplaceAll(c.Name, "/", ".") + "-" + version
}
