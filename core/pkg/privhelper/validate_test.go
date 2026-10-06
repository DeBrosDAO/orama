package privhelper

import (
	"strings"
	"testing"
)

// Every command an unprivileged Orama process actually runs as root today.
// If one of these is refused, the node fails at the moment it needs it — a
// namespace that will not start, a TURN port that stays closed.
func TestValidate_globalUnitsAreExact(t *testing.T) {
	for _, unit := range []string{
		"orama-global-chain.service",
		"orama-global-ipfs.service",
		"orama-global-ipfs-gc.timer",
		"orama-global-provider.service",
		"orama-global-tor-relay.service",
		"orama-global-tor-dirauth.service",
		"orama-global-sbws.service",
		"orama-global-reporter.service",
		"orama-global-tor-onion.service",
		"orama-global-archiver.service",
		"orama-global-repair.service",
		"orama-global-relay.service",
	} {
		for _, verb := range []string{"start", "stop", "restart", "status", "is-active"} {
			if _, err := Validate([]string{"systemctl", verb, unit}); err != nil {
				t.Errorf("systemctl %s %s: %v", verb, unit, err)
			}
		}
		if _, err := Validate([]string{"systemctl", "enable", unit}); err == nil {
			t.Errorf("enable %s was allowed", unit)
		}
	}
	if _, err := Validate([]string{"systemctl", "start", "orama-global-evil.service"}); err == nil {
		t.Fatal("orama-global-evil was accepted")
	}
	if _, err := Validate([]string{"systemctl", "status", "orama-namespace-gateway@index.service"}); err == nil {
		t.Fatal("status was allowed on a cluster unit")
	}
}

func TestValidate_AllowsWhatTheNodeRuns(t *testing.T) {
	for _, argv := range [][]string{
		{"systemctl", "daemon-reload"},
		{"systemctl", "start", "orama-namespace-gateway@anchat-v2.service"},
		{"systemctl", "restart", "orama-namespace-rqlite@index.service"},
		{"systemctl", "stop", "orama-namespace-olric@index"},
		{"systemctl", "enable", "orama-namespace-ipfs-gc@index.timer"},
		{"systemctl", "disable", "orama-namespace-wireguard@index.service"},
		{"systemctl", "start", "orama-deploy-node@acme-web.service"},
		{"systemctl", "reset-failed", "orama-deploy-build@acme-web.service"},
		{"systemctl", "restart", "orama-deploy-go@my_ns-api-v2.service"},
		{"systemctl", "set-property", "orama-deploy-npm@acme-web.service", "MemoryMax=512M"},
		{"systemctl", "set-property", "orama-deploy-npm@acme-web.service", "MemoryMax=512M", "CPUQuota=150%"},
		{"systemctl", "start", "orama-turn.service"},
		{"systemctl", "enable", "orama-turn.service"},
		{"systemctl", "stop", "orama-olric.service"},
		{"systemctl", "disable", "wg-quick@wg0.service"},
		{"systemctl", "stop", "coredns.service"},
		{"ufw", "status"},
		{"ufw", "status", "verbose"},
		{"ufw", "reload"},
		{"ufw", "allow", "3478/udp"},
		{"ufw", "allow", "3478/tcp"},
		{"ufw", "allow", "5349/tcp"},
		{"ufw", "allow", "49152:65535/udp"},
		{"ufw", "allow", "3478/udp", "comment", "orama"},
		{"ufw", "allow", "49152:65535/udp", "comment", "orama"},
		{"ufw", "delete", "allow", "50000:50999/udp"},
		{"ufw", "delete", "allow", "3478/udp", "comment", "orama"},
	} {
		inv, err := Validate(argv)
		if err != nil {
			t.Errorf("%q refused: %v", argv, err)
			continue
		}
		if inv.Tool != argv[0] || strings.Join(inv.Args, " ") != strings.Join(argv[1:], " ") {
			t.Errorf("%q validated into %+v", argv, inv)
		}
	}
}

// What the old wildcard rules let through, and what a compromised orama user
// would try next.
func TestValidate_RefusesEverythingElse(t *testing.T) {
	for _, argv := range [][]string{
		{},
		{"bash", "-c", "id"},
		{"/usr/bin/systemctl", "daemon-reload"},
		{"systemctl"},
		{"systemctl", "start"},
		{"systemctl", "start", "ssh.service"},
		{"systemctl", "stop", "sshd"},
		{"systemctl", "mask", "orama-namespace-gateway@x.service"},
		{"systemctl", "edit", "orama-namespace-gateway@x.service"},
		{"systemctl", "start", "orama-namespace-gateway@x.service", "ssh.service"},
		{"systemctl", "start", "orama-namespace-gateway@x.service", "--root=/tmp/evil"},
		{"systemctl", "--root=/tmp/evil", "start", "orama-namespace-gateway@x.service"},
		{"systemctl", "start", "orama-namespace-gateway@x y.service"},
		{"systemctl", "start", "orama-namespace-gateway@../../etc.service"},
		{"systemctl", "start", "orama-namespace-gateway@-x.service"},
		{"systemctl", "start", "orama-namespace-gateway@x.socket"},
		{"systemctl", "start", "orama-deploy-node@acme.service.d"},
		{"systemctl", "start", "orama-olric.service"},
		{"systemctl", "enable", "caddy.service"},
		{"systemctl", "set-property", "orama-namespace-gateway@x.service", "MemoryMax=1G"},
		{"systemctl", "set-property", "orama-deploy-node@a-b.service", "ExecStart=/bin/sh"},
		{"systemctl", "set-property", "orama-deploy-node@a-b.service", "MemoryMax=1G", "MemoryMax=2G"},
		{"systemctl", "set-property", "orama-deploy-node@a-b.service", "MemoryMax=infinity"},
		{"systemctl", "set-property", "orama-deploy-node@a-b.service", "CPUQuota=0%"},
		{"systemctl", "set-property", "orama-deploy-node@a-b.service"},
		{"systemctl", "daemon-reload", "extra"},
		{"ufw", "disable"},
		{"ufw", "reset"},
		{"ufw", "--force", "reset"},
		{"ufw", "allow", "22/tcp"},
		{"ufw", "allow", "10100/tcp"},
		{"ufw", "allow", "from", "1.2.3.4"},
		{"ufw", "allow", "1:65535/udp"},
		{"ufw", "allow", "49152:65535/tcp"},
		{"ufw", "allow", "60000:50000/udp"},
		{"ufw", "allow", "49152:70000/udp"},
		{"ufw", "allow", "3478/udp", "extra"},
		{"ufw", "allow", "3478/udp", "comment", "other"},
		{"ufw", "allow", "3478/udp", "comment", "orama", "extra"},
		{"ufw", "allow", "22/tcp", "comment", "orama"},
		{"ufw", "delete", "allow", "22/tcp", "comment", "orama"},
		{"ufw", "delete", "allow", "22/tcp"},
		{"ufw", "default", "allow", "incoming"},
	} {
		if inv, err := Validate(argv); err == nil {
			t.Errorf("%q must be refused, validated into %+v", argv, inv)
		}
	}
}

// An unprivileged caller never runs sudo — no_new_privs makes it useless in
// orama-node's unit — it hands the request to the root helper's socket.
func TestCommand_UnprivilegedCallerGoesThroughTheSocketClient(t *testing.T) {
	cmd := Command("systemctl", "daemon-reload")
	if cmd.Args[0] == "systemctl" {
		t.Skip("running as root: tools run directly by design")
	}
	if want := Path + " call systemctl daemon-reload"; strings.Join(cmd.Args, " ") != want {
		t.Errorf("Command args = %q, want %q", strings.Join(cmd.Args, " "), want)
	}
}

func TestSocketUnit_OnlyRootAndTheOramaGroupCanConnect(t *testing.T) {
	for _, want := range []string{"ListenStream=" + SocketPath, "SocketUser=root", "SocketGroup=orama", "SocketMode=0660", "Accept=yes"} {
		if !strings.Contains(SocketUnit, want) {
			t.Errorf("socket unit missing %q", want)
		}
	}
	if !strings.Contains(ServiceUnit, "ExecStart="+Path+" serve") || !strings.Contains(ServiceUnit, "StandardInput=socket") {
		t.Errorf("service unit does not serve the connection: %s", ServiceUnit)
	}
}

// Stopping wg-quick@wg0 runs wg-quick down and cuts the node off the mesh; the
// index migration only ever disables it.
func TestValidate_WGQuickMayOnlyBeDisabled(t *testing.T) {
	if _, err := Validate([]string{"systemctl", "disable", "wg-quick@wg0.service"}); err != nil {
		t.Fatalf("disable must be allowed: %v", err)
	}
	if _, err := Validate([]string{"systemctl", "stop", "wg-quick@wg0.service"}); err == nil {
		t.Fatal("stop must be refused")
	}
}

// tor runs as debian-tor and ntfy as ntfy; an env file the orama user could
// write for them would carry LD_PRELOAD into a process it does not own.
func TestValidate_unitEnvRefusesUnitsNotRunAsOrama(t *testing.T) {
	for _, svc := range []string{"tor", "ntfy", "wireguard"} {
		if _, err := Validate([]string{ToolUnitEnv, "set", "index", svc}); err == nil {
			t.Errorf("unitenv set index %s was allowed", svc)
		}
	}
	for _, svc := range []string{"gateway", "rqlite", "olric"} {
		if _, err := Validate([]string{ToolUnitEnv, "set", "index", svc}); err != nil {
			t.Errorf("unitenv set index %s refused: %v", svc, err)
		}
	}
	if _, err := Validate([]string{ToolUnitEnv, "clear", "index"}); err != nil {
		t.Errorf("unitenv clear refused: %v", err)
	}
	if _, err := Validate([]string{ToolUnitEnv, "clear", "acme", "sfu"}); err != nil {
		t.Errorf("unitenv clear of one service refused: %v", err)
	}
	for _, bad := range [][]string{{"clear", "../x", "sfu"}, {"clear", "acme", "../sfu"}, {"clear", "acme", "sfu", "x"}} {
		if _, err := Validate(append([]string{ToolUnitEnv}, bad...)); err == nil {
			t.Errorf("unitenv %v was allowed", bad)
		}
	}
}

// Teardown disables with --no-reload so systemd does not reload every unit per
// service. That is the only flag the helper takes, and only on disable, before
// the unit.
func TestValidate_disableNoReload(t *testing.T) {
	for _, unit := range []string{
		"orama-namespace-rqlite@acme.service",
		"orama-namespace-gateway@acme",
		"orama-deploy-node@acme-web.service",
		"orama-turn.service",
		"orama-olric.service",
		"wg-quick@wg0.service",
	} {
		inv, err := Validate([]string{"systemctl", "disable", "--no-reload", unit})
		if err != nil {
			t.Errorf("disable --no-reload %s refused: %v", unit, err)
			continue
		}
		if len(inv.Args) != 3 || inv.Args[1] != NoReloadFlag {
			t.Errorf("args = %q, want them passed through unchanged", inv.Args)
		}
	}
}

func TestValidate_disableNoReloadKeepsTheUnitRules(t *testing.T) {
	for _, argv := range [][]string{
		{"systemctl", "disable", "--no-reload", "sshd.service"},
		{"systemctl", "disable", "--no-reload", "orama-namespace-rqlite@../x.service"},
		{"systemctl", "disable", "--no-reload", "orama-global-chain.service"},
		{"systemctl", "disable", "--no-reload", ""},
		// Flag in another position, other flags, other verbs.
		{"systemctl", "disable", "orama-turn.service", "--no-reload"},
		{"systemctl", "--no-reload", "disable", "orama-turn.service"},
		{"systemctl", "disable", "--now", "orama-turn.service"},
		{"systemctl", "disable", "--no-reload", "--now", "orama-turn.service"},
		{"systemctl", "disable", "--now", "--no-reload", "orama-turn.service"},
		{"systemctl", "disable", "--no-reload=yes", "orama-turn.service"},
		{"systemctl", "disable", "--NO-RELOAD", "orama-turn.service"},
		{"systemctl", "disable", "--no-reload"},
		{"systemctl", "disable", "--no-reload", "orama-turn.service", "orama-olric.service"},
		{"systemctl", "enable", "--no-reload", "orama-turn.service"},
		{"systemctl", "stop", "--no-reload", "orama-turn.service"},
		{"systemctl", "start", "--no-reload", "orama-turn.service"},
		{"systemctl", "daemon-reload", "--no-reload"},
		{"systemctl", "mask", "--no-reload", "orama-turn.service"},
	} {
		if _, err := Validate(argv); err == nil {
			t.Errorf("%q was allowed", argv)
		}
	}
	// wg-quick may still only be disabled.
	if _, err := Validate([]string{"systemctl", "stop", "--no-reload", "wg-quick@wg0.service"}); err == nil {
		t.Error("stop --no-reload wg-quick was allowed")
	}
}

func TestValidate_resetFailedOnlyOnDeploymentAndNamespaceUnits(t *testing.T) {
	for _, unit := range []string{"sshd.service", "wg-quick@wg0.service", "orama-olric.service", "orama-global-chain.service"} {
		if _, err := Validate([]string{"systemctl", "reset-failed", unit}); err == nil {
			t.Errorf("reset-failed %s was allowed", unit)
		}
	}
	if _, err := Validate([]string{"systemctl", "reset-failed"}); err == nil {
		t.Error("reset-failed without a unit was allowed")
	}
}
