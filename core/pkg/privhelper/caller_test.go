package privhelper

import (
	"strings"
	"testing"
)

func mustValidate(t *testing.T, argv ...string) Invocation {
	t.Helper()
	inv, err := Validate(argv)
	if err != nil {
		t.Fatalf("%q: %v", argv, err)
	}
	return inv
}

const oramaUID = 998

// The finding: the uid was the whole check, so every orama process — a tenant's
// gateway running tenant WASM, the internet-facing Caddy — could rewrite the
// mesh and any namespace's units.
func TestAuthorize_refusesEveryOtherOramaProcess(t *testing.T) {
	for _, unit := range []string{
		"orama-namespace-gateway@alice.service",
		"orama-namespace-caddy@index.service",
		"orama-namespace-pubsub@index.service",
		"orama-deploy-node@alice-web.service",
		"",
	} {
		for _, argv := range [][]string{
			{"wireguard", "persist-peers"},
			{"unitenv", "set", "alice", "gateway"},
			{"systemctl", "start", "orama-namespace-gateway@alice.service"},
			{"deploy", "set-env", "alice-web"},
			{"ufw", "allow", "3478/udp"},
		} {
			if err := Authorize(Caller{UID: oramaUID, Unit: unit}, mustValidate(t, argv...)); err == nil {
				t.Errorf("%q was allowed %q", unit, argv)
			}
		}
	}
}

// The node supervisor keeps everything it uses.
func TestAuthorize_theNodeKeepsWhatItRuns(t *testing.T) {
	for _, argv := range [][]string{
		{"wireguard", "persist-peers"},
		{"wireguard", "remove-peer", "10.0.0.9/32"},
		{"systemctl", "restart", "orama-namespace-rqlite@index.service"},
		{"systemctl", "disable", "wg-quick@wg0.service"},
		{"systemctl", "stop", "orama-olric.service"},
		{"unitenv", "set", "index", "pubsub"},
		{"deploy", "set-token", "alice-web"},
	} {
		if err := Authorize(Caller{UID: oramaUID, Unit: NodeUnit}, mustValidate(t, argv...)); err != nil {
			t.Errorf("orama-node refused %q: %v", argv, err)
		}
	}
}

// The cluster gateway keeps what its cluster manager, deployment runner and
// join handlers use, and loses the whole-mesh rewrite and the node's migrations.
func TestAuthorize_theClusterGateway(t *testing.T) {
	allowed := [][]string{
		{"systemctl", "daemon-reload"},
		{"systemctl", "start", "orama-namespace-gateway@alice.service"},
		{"systemctl", "stop", "orama-namespace-rqlite@alice.service"},
		{"systemctl", "start", "orama-turn.service"},
		{"systemctl", "restart", "orama-deploy-node@alice-web.service"},
		{"systemctl", "set-property", "orama-deploy-npm@alice-web.service", "MemoryMax=512M"},
		{"unitenv", "set", "alice", "gateway"},
		{"unitenv", "clear", "alice"},
		{"deploy", "set-env", "alice-web"},
		{"ufw", "allow", "49152:65535/udp"},
		// A join, an enrolment and a node removal act on one peer at once.
		{"wireguard", "add-peer", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", "203.0.113.1:51820", "10.0.0.9/32"},
		{"wireguard", "remove-peer", "10.0.0.9/32"},
	}
	for _, argv := range allowed {
		if err := Authorize(Caller{UID: oramaUID, Unit: IndexGatewayUnit}, mustValidate(t, argv...)); err != nil {
			t.Errorf("the cluster gateway was refused %q: %v", argv, err)
		}
	}
	refused := [][]string{
		{"wireguard", "persist-peers"},
		{"systemctl", "disable", "wg-quick@wg0.service"},
		{"systemctl", "stop", "caddy.service"},
	}
	for _, argv := range refused {
		if err := Authorize(Caller{UID: oramaUID, Unit: IndexGatewayUnit}, mustValidate(t, argv...)); err == nil {
			t.Errorf("the cluster gateway was allowed %q", argv)
		}
	}
}

func TestAuthorize_rootMayDoAnything(t *testing.T) {
	if err := Authorize(Caller{UID: 0}, mustValidate(t, "wireguard", "persist-peers")); err != nil {
		t.Error(err)
	}
}

func TestUnitFromCgroup(t *testing.T) {
	for content, want := range map[string]string{
		"0::/system.slice/orama-node.service\n": NodeUnit,
		"0::/system.slice/system-orama\\x2dnamespace\\x2dgateway.slice/orama-namespace-gateway@index.service\n": IndexGatewayUnit,
		"0::/system.slice/orama-node.service/sub\n":                                                             NodeUnit,
		"12:pids:/system.slice/x.service\n0::/system.slice/orama-node.service\n":                                NodeUnit,
	} {
		got, err := UnitFromCgroup(content)
		if err != nil || got != want {
			t.Errorf("UnitFromCgroup(%q) = %q, %v; want %q", content, got, err, want)
		}
	}
	for _, content := range []string{
		"0::/user.slice/user-1000.slice/session-3.scope\n",
		"0::/user.slice/user-1000.slice/user@1000.service/app.slice/orama-node.service\n",
		"0::/system.slice/../user.slice/user@1000.service\n",
		"0::/system.slice//orama-node.service\n",
		"1:name=systemd:/system.slice/orama-node.service\n",
		"",
	} {
		if got, err := UnitFromCgroup(content); err == nil {
			t.Errorf("UnitFromCgroup(%q) = %q; want an error", content, got)
		} else if strings.TrimSpace(err.Error()) == "" {
			t.Error("empty error")
		}
	}
}
