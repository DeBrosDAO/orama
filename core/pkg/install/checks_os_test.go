package install

import (
	"strings"
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
		{ID: "ubuntu", Version: "24.10"}, // interim, past end of life
		{ID: "ubuntu", Version: "25.10"}, // interim, past end of life
		{ID: "debian", Version: "11"},
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

// The current Ubuntu LTS and the current Debian stable are what a fresh VPS
// ships with; refusing them sent operators to an end-of-life release.
func TestIsSupportedOS_currentLTSAndStableAreSupported(t *testing.T) {
	od := &OSDetector{}
	for _, info := range []OSInfo{
		{ID: "ubuntu", Version: "26.04"},
		{ID: "debian", Version: "13"},
	} {
		info := info
		if !od.IsSupportedOS(&info) {
			t.Errorf("%s %s must be supported", info.ID, info.Version)
		}
	}
}

func TestSupportedReleasesText_namesEverySupportedRelease(t *testing.T) {
	text := SupportedReleasesText()
	for id, versions := range supportedReleases {
		for version := range versions {
			if !strings.Contains(text, version) || !strings.Contains(text, id) {
				t.Errorf("%q does not name %s %s", text, id, version)
			}
		}
	}
	if want := "debian 12, 13; ubuntu 22.04, 24.04, 26.04"; text != want {
		t.Errorf("SupportedReleasesText() = %q, want %q (sorted, stable)", text, want)
	}
}
