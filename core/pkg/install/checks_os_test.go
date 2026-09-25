package install

import (
	"testing"

	"github.com/DeBrosOfficial/network/pkg/install/installers"
)

// A release the installer accepts but the Tor Project does not publish for
// would pass Phase 1 with only a warning and then fail Phase 2d.
func TestIsSupportedOS_everySupportedReleaseHasATorSuite(t *testing.T) {
	od := &OSDetector{}
	for id, versions := range supportedReleases {
		for version, codename := range versions {
			if !od.IsSupportedOS(&OSInfo{ID: id, Version: version}) {
				t.Errorf("%s %s should be supported", id, version)
			}
			if _, err := installers.TorSuiteFor(codename); err != nil {
				t.Errorf("%s %s (%s): %v", id, version, codename, err)
			}
		}
	}
}

func TestIsSupportedOS_unlistedReleasesAreRefused(t *testing.T) {
	od := &OSDetector{}
	for _, info := range []OSInfo{
		{ID: "ubuntu", Version: "25.04"}, // no Tor Project suite
		{ID: "ubuntu", Version: "20.04"},
		{ID: "fedora", Version: "40"},
		{},
	} {
		info := info
		if od.IsSupportedOS(&info) {
			t.Errorf("%q %q must not be supported", info.ID, info.Version)
		}
	}
}
