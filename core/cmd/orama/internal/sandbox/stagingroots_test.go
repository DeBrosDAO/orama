package sandbox

import (
	"os"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/cmd/orama/internal"
)

// The embedded bundle is exactly the two pinned Let's Encrypt staging roots.
func TestStagingRoots_areExactlyThePinnedRoots(t *testing.T) {
	if err := checkStagingRoots(stagingRootsPEM); err != nil {
		t.Fatal(err)
	}
}

// A bundle with a root missing, or one added, is refused: the pin is the list.
func TestCheckStagingRoots_refusesAnythingElse(t *testing.T) {
	first := stagingRootsPEM[:strings.Index(string(stagingRootsPEM), "-----END CERTIFICATE-----")+len("-----END CERTIFICATE-----\n")]
	for name, bundle := range map[string][]byte{
		"empty":        nil,
		"one root":     first,
		"a root twice": append(append([]byte{}, stagingRootsPEM...), first...),
		"not PEM":      []byte("not a certificate"),
	} {
		if err := checkStagingRoots(bundle); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// Create records the sandbox environment with the staging roots as its CA, so
// the CLI can reach a cluster whose certificates come from staging, and makes
// it the active environment.
func TestRegisterEnvironment_trustsTheStagingRootsForTheSandboxDomain(t *testing.T) {
	state := testSandbox(t)
	cfg := &Config{Domain: "sbx.example.com"}

	if err := registerEnvironment(cfg, state); err != nil {
		t.Fatal(err)
	}
	env, err := cli.GetActiveEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	if env.Name != "sandbox" || env.GatewayURL != "https://sbx.example.com" {
		t.Fatalf("active environment = %+v, want sandbox at https://sbx.example.com", env)
	}
	data, err := os.ReadFile(env.CAFile)
	if err != nil {
		t.Fatalf("the sandbox environment's CA file: %v", err)
	}
	if string(data) != string(stagingRootsPEM) {
		t.Fatal("the sandbox environment's CA file is not the staging roots")
	}
	if err := cli.TrustEnvironmentCAs(); err != nil {
		t.Fatalf("the CLI cannot load the sandbox CA: %v", err)
	}
}
