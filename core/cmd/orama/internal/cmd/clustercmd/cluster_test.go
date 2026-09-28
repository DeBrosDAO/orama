package clustercmd

import (
	"testing"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/gateway/handlers/operator"
)

func TestParseClusterSetting(t *testing.T) {
	path, body, err := parseClusterSetting("namespace-creation", " open ")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/v1/operator/settings/namespace-creation" {
		t.Fatalf("path %q", path)
	}
	got, _ := body.(map[string]string)
	if got["value"] != operator.CreationOpen {
		t.Fatalf("body %#v", body)
	}

	for _, value := range []string{"everyone", "Open", ""} {
		if _, _, err := parseClusterSetting("namespace-creation", value); clierr.CodeOf(err) != clierr.CodeUsage {
			t.Errorf("%q: %v, want a usage error", value, err)
		}
	}

	_, capBody, err := parseClusterSetting("max-namespaces-per-wallet", "11")
	if err != nil {
		t.Fatal(err)
	}
	n, _ := capBody.(map[string]any)
	if n["value"] != 11 {
		t.Fatalf("cap body %#v", capBody)
	}
	for _, value := range []string{"0", "-1", "10001", "nope"} {
		if _, _, err := parseClusterSetting("max-namespaces-per-wallet", value); clierr.CodeOf(err) != clierr.CodeUsage {
			t.Errorf("%q: %v, want a usage error", value, err)
		}
	}

	if _, _, err := parseClusterSetting("replicas", "3"); clierr.CodeOf(err) != clierr.CodeUsage {
		t.Fatalf("unknown setting: %v", err)
	}
}
