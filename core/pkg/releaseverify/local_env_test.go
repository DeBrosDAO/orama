package releaseverify

import "testing"

// A release build never has the localrepo tag, so the environment cannot lift the check that keeps
// the agent (root, on a cluster's overlay) off this machine and its private networks.
func TestParseRepositoryURL_theEnvironmentOpensNothingWithoutTheLocalrepoTag(t *testing.T) {
	t.Setenv(AllowLocalEnv, "1")
	for _, raw := range []string{"http://127.0.0.1:18081", "https://127.0.0.1/releases", "https://10.0.0.1/releases", "https://localhost/r"} {
		_, err := ParseRepositoryURL(raw)
		if localAllowedByEnv() {
			if err != nil {
				t.Errorf("built with the localrepo tag, the environment should allow %q: %v", raw, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s was accepted because of %s, in a binary built without the localrepo tag", raw, AllowLocalEnv)
		}
	}
}

func TestAllowLocalRepositories_endsWithTheTest(t *testing.T) {
	t.Run("inside", func(t *testing.T) {
		AllowLocalRepositories(t)
		if _, err := ParseRepositoryURL("http://127.0.0.1:18081"); err != nil {
			t.Errorf("a test that asked for local repositories was refused: %v", err)
		}
	})
	if _, err := ParseRepositoryURL("http://127.0.0.1:18081"); err == nil && !localAllowedByEnv() {
		t.Error("the permission outlived the test that asked for it")
	}
}
