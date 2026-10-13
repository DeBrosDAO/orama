package namespace

import (
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/gatewayspec"
)

// A namespace gateway never serves /v1/proxy/relay: the route is a MainGateway
// route, answered by the index gateway on every host. The allowlist therefore
// is not written into a namespace gateway's YAML (bugboard #266).
func TestGatewayYAMLFromInstance_carriesNoRelayAllowlist(t *testing.T) {
	cfg := gatewayspec.InstanceConfig{
		Namespace: "anchat", RQLiteDSN: "http://10.0.0.1:10005", BaseDomain: "dbrs.space",
		OlricTimeout: time.Second, RelayAllowedSuffixes: []string{"partner.example"},
	}
	y := gatewayYAMLFromInstance(cfg, "hmac", "/secret", "10.0.0.1:6001")
	if len(y.RelayAllowedSuffixes) != 0 {
		t.Errorf("a namespace gateway's YAML carries relay_allowed_suffixes: %v", y.RelayAllowedSuffixes)
	}
}
