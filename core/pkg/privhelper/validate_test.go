package privhelper

import (
	"strings"
	"testing"
)

// Every command an unprivileged Orama process actually runs as root today.
// If one of these is refused, the node fails at the moment it needs it — a
// namespace that will not start, a TURN port that stays closed.
func TestValidate_AllowsWhatTheNodeRuns(t *testing.T) {
	for _, argv := range [][]string{
		{"systemctl", "daemon-reload"},
		{"systemctl", "start", "orama-namespace-gateway@anchat-v2.service"},
		{"systemctl", "restart", "orama-namespace-rqlite@index.service"},
		{"systemctl", "stop", "orama-namespace-olric@index"},
		{"systemctl", "enable", "orama-namespace-ipfs-gc@index.timer"},
		{"systemctl", "disable", "orama-namespace-wireguard@index.service"},
		{"systemctl", "start", "orama-deploy-node@acme-web.service"},
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
		{"ufw", "delete", "allow", "50000:50999/udp"},
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
		{"ufw", "delete", "allow", "22/tcp"},
		{"ufw", "default", "allow", "incoming"},
	} {
		if inv, err := Validate(argv); err == nil {
			t.Errorf("%q must be refused, validated into %+v", argv, inv)
		}
	}
}

// The grant names the helper and nothing else: no argument pattern for sudo-rs
// to reject, no wildcard for classic sudo to over-match.
func TestSudoersRule_GrantsOnlyTheHelperWithoutWildcards(t *testing.T) {
	rule := SudoersRule("orama")
	if rule != "orama ALL=(root) NOPASSWD: /usr/local/bin/orama-privhelper\n" {
		t.Fatalf("unexpected rule %q", rule)
	}
	if strings.ContainsAny(rule, "*?[") {
		t.Errorf("rule %q carries a wildcard", rule)
	}
}

func TestCommand_GoesThroughSudoNonInteractivelyWhenNotRoot(t *testing.T) {
	cmd := Command("systemctl", "daemon-reload")
	args := strings.Join(cmd.Args, " ")
	if cmd.Args[0] == "systemctl" {
		t.Skip("running as root: the helper is bypassed by design")
	}
	if want := "sudo -n " + Path + " systemctl daemon-reload"; args != want {
		t.Errorf("Command args = %q, want %q", args, want)
	}
}
