package stages

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/manifest"
)

// secretEnv is what `infisical run` puts in the runner's environment.
var secretEnv = []string{
	"HCLOUD_TOKEN=hc-secret-value", "CF_API_TOKEN=cf-secret-value", "CF_ZONE=dbrsteting.bid",
	"INFISICAL_TOKEN=inf-secret", "INFISICAL_CLIENT_ID=inf-id", "INFISICAL_ANYTHING=x",
	"BUGBOARD_MCP_TOKEN=bb-secret", "RW_TEST_MNEMONIC=words words words", "SSH_AUTH_SOCK=/tmp/agent",
	"HOME=/Users/owner", "XDG_CONFIG_HOME=/Users/owner/.config", "AWS_SECRET_ACCESS_KEY=aws",
}

func TestFeatureEnv_dropsSecretsKeepsAllowlist(t *testing.T) {
	in := append([]string{"PATH=/bin", "GOCACHE=/c", "GOMODCACHE=/m", "GOPATH=/g", "TMPDIR=/t",
		"E2E_PACE_CRED_BURST=4", "HTTPS_PROXY=http://proxy:3128", "LANG=C.UTF-8"}, secretEnv...)
	got := strings.Join(FeatureEnv(in), "\n")
	for _, want := range []string{"PATH=/bin", "GOCACHE=/c", "GOMODCACHE=/m", "GOPATH=/g", "TMPDIR=/t",
		"E2E_PACE_CRED_BURST=4", "HTTPS_PROXY=http://proxy:3128", "LANG=C.UTF-8"} {
		if !strings.Contains(got, want) {
			t.Errorf("%s dropped from %q", want, got)
		}
	}
	for _, kv := range secretEnv {
		if strings.Contains(got, kv) {
			t.Errorf("%s reached the feature environment", kv)
		}
	}
}

func TestFeatureEnv_emptyAndMalformed(t *testing.T) {
	if got := FeatureEnv(nil); len(got) != 0 {
		t.Fatalf("nil environ gave %v", got)
	}
	if got := FeatureEnv([]string{"PATH", "=x", "E2E_STRICT=1"}); strings.Join(got, ",") != "E2E_STRICT=1" {
		t.Fatalf("got %v", got)
	}
}

// TestFeatureEnv_explicitE2ENamesOnly: an E2E_* variable not on the list
// (the runner's own provisioning settings) never reaches a feature.
func TestFeatureEnv_explicitE2ENamesOnly(t *testing.T) {
	got := strings.Join(FeatureEnv([]string{"E2E_RW_BIN=/x/rw", "E2E_RUNNER_CIDR=1.2.3.4/32", "E2E_SOAK_MINUTES=5", "E2E_SEALED_FD=3"}), ",")
	if got != "E2E_SOAK_MINUTES=5" {
		t.Fatalf("got %q", got)
	}
}

// TestFeatureEnv_proxyCredentialsStripped: a proxy URL keeps its host and
// loses its user:password; an unparsable one with an @ is dropped.
func TestFeatureEnv_proxyCredentialsStripped(t *testing.T) {
	got := strings.Join(FeatureEnv([]string{"HTTPS_PROXY=http://bob:s3cret@proxy:3128", "http_proxy=::bad@", "NO_PROXY=localhost,.internal"}), "\n")
	if strings.Contains(got, "s3cret") || strings.Contains(got, "bob") || !strings.Contains(got, "HTTPS_PROXY=http://proxy:3128") ||
		strings.Contains(got, "http_proxy") || !strings.Contains(got, "NO_PROXY=localhost,.internal") {
		t.Fatalf("got %q", got)
	}
}

// TestFeatureEnv_goSourceSettingsGated: GOFLAGS, GOPROXY and the checksum
// settings pass only with E2E_ALLOW_GO_ENV=1.
func TestFeatureEnv_goSourceSettingsGated(t *testing.T) {
	in := []string{"GOFLAGS=-mod=mod", "GOPROXY=http://evil", "GOSUMDB=off", "GONOSUMDB=*", "GOCACHE=/c"}
	if got := strings.Join(FeatureEnv(in), ","); got != "GOCACHE=/c" {
		t.Fatalf("without the opt-in: %q", got)
	}
	if got := FeatureEnv(append(in, EnvAllowGoEnv+"=1")); len(got) != 5 {
		t.Fatalf("with the opt-in: %q", got)
	}
}

// TestRun_featureProcessNeverSeesCloudTokens is the regression test for the
// runner passing all of os.Environ to feature packages.
func TestRun_featureProcessNeverSeesCloudTokens(t *testing.T) {
	fe := &fakeExec{}
	r := newRunner(t, fe)
	r.BaseEnv = append([]string{"PATH=/bin"}, secretEnv...)
	r.ExtraEnv = []string{"E2E_BROKER_SOCK=/run/broker/broker.sock"}
	steps, _ := Plan(testStages(), []manifest.Manifest{{ID: "a", Stage: 1}})
	if _, err := r.Run(context.Background(), steps, Options{}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Rerun(context.Background(), []FailedTest{{Feature: "a", Test: "TestX"}}, Duration(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if len(fe.envs) != 2 {
		t.Fatalf("want a run and a re-run, got %d", len(fe.envs))
	}
	for _, env := range fe.envs {
		joined := strings.Join(env, "\n")
		for _, name := range []string{"HCLOUD_TOKEN", "CF_API_TOKEN", "INFISICAL_", "HOME=", "SSH_AUTH_SOCK"} {
			if strings.Contains(joined, name) {
				t.Errorf("%s reached a feature process: %q", name, joined)
			}
		}
		if !strings.Contains(joined, "E2E_BROKER_SOCK=/run/broker/broker.sock") || !strings.Contains(joined, "PATH=/bin") {
			t.Errorf("the broker socket or PATH is missing: %q", joined)
		}
	}
}
