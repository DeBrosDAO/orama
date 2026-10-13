package install

import (
	"strings"
	"testing"
)

func TestValidateUpdatePolicy_genesisAcceptsRepoAndChannel(t *testing.T) {
	for _, f := range []Flags{
		{},
		{ReleaseRepo: "https://test-releases.example.org", UpdateChannel: "nightly"},
		{ReleaseRepo: "https://test-releases.example.org"},
		{UpdateChannel: "dev/my-branch"},
	} {
		if err := f.validateUpdatePolicy(); err != nil {
			t.Errorf("%+v refused: %v", f, err)
		}
	}
}

func TestValidateUpdatePolicy_refusesWhatTheClusterWouldRefuse(t *testing.T) {
	for name, c := range map[string]struct {
		f    Flags
		want string
	}{
		"http repo":   {Flags{ReleaseRepo: "http://releases.example.org"}, "--release-repo"},
		"not a URL":   {Flags{ReleaseRepo: "releases"}, "--release-repo"},
		"bad channel": {Flags{UpdateChannel: "Nightly Build"}, "--update-channel"},
	} {
		err := c.f.validateUpdatePolicy()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want one naming %s", name, err, c.want)
		}
	}
}

func TestValidateUpdatePolicy_aJoinTakesNoSettings(t *testing.T) {
	f := Flags{JoinAddress: "https://node0.example.com", Token: "tok", UpdateChannel: "nightly"}
	err := f.validateUpdatePolicy()
	if err == nil || !strings.Contains(err.Error(), "joining node") {
		t.Errorf("err = %v", err)
	}
	f = Flags{JoinAddress: "https://node0.example.com", Token: "tok"}
	if err := f.validateUpdatePolicy(); err != nil {
		t.Errorf("a join with no settings refused: %v", err)
	}
}

func TestRemoteInstallArgs_noSettingsNoFlags(t *testing.T) {
	line := strings.Join(remoteInstallArgs(&Flags{VpsIP: "1.2.3.4"}), " ")
	if strings.Contains(line, "release-repo") || strings.Contains(line, "update-channel") {
		t.Fatalf("settings nobody set are forwarded: %q", line)
	}
}
