//go:build e2e_fleet

package deployments

import "testing"

func TestBuildInstallFinished(t *testing.T) {
	const finished = "Starting orama-deploy-build@ns-app.service - Orama Deployment dependency install - ns-app...\n" +
		"added 1 package in 751ms\n" +
		"orama-deploy-build@ns-app.service: Deactivated successfully.\n" +
		"Finished orama-deploy-build@ns-app.service - Orama Deployment dependency install - ns-app.\n"
	const failed = "Starting orama-deploy-build@ns-app.service - Orama Deployment dependency install - ns-app...\n" +
		"orama-deploy-build@ns-app.service: Main process exited, code=exited, status=254/n/a\n" +
		"orama-deploy-build@ns-app.service: Failed with result 'exit-code'.\n" +
		"Failed to start orama-deploy-build@ns-app.service - Orama Deployment dependency install - ns-app.\n"
	for name, c := range map[string]struct {
		journal string
		want    bool
	}{
		"success":      {finished, true},
		"failure":      {failed, false},
		"empty":        {"", false},
		"never loaded": {"-- No entries --\n", false},
	} {
		if got := buildInstallFinished(c.journal); got != c.want {
			t.Errorf("%s: got %v, want %v", name, got, c.want)
		}
	}
}
