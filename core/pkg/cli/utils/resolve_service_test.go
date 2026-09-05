package utils

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// installUnits writes empty unit files into a directory the resolver reads for
// the length of one test, standing in for /etc/systemd/system.
//
// It swaps a package variable, so a test that calls it must not call
// t.Parallel(): two parallel tests would race on the directory and read each
// other's units.
func installUnits(t *testing.T, units ...string) {
	t.Helper()
	dir := t.TempDir()
	for _, unit := range units {
		if err := os.WriteFile(filepath.Join(dir, unit), nil, 0644); err != nil {
			t.Fatalf("write %s: %v", unit, err)
		}
	}
	previous := systemdUnitDir
	systemdUnitDir = dir
	t.Cleanup(func() { systemdUnitDir = previous })
}

// A tenant service is a template instance. It has no unit file of its own —
// systemd instantiates it from orama-namespace-olric@.service — so a resolver
// that stats the instance name reports every tenant service on the node as
// missing, which is what broke `orama node logs orama-namespace-olric@<ns>`.
func TestResolveServiceName_templateInstance(t *testing.T) {
	installUnits(t, "orama-namespace-olric@.service")

	for _, alias := range []string{
		"orama-namespace-olric@anchat",
		"orama-namespace-olric@anchat.service",
	} {
		unit, err := ResolveServiceName(alias)
		if err != nil {
			t.Fatalf("%s: %v", alias, err)
		}
		if unit != "orama-namespace-olric@anchat" {
			t.Errorf("%s resolved to %q", alias, unit)
		}
	}
}

func TestResolveServiceName_templateInstanceWithoutTheTemplate(t *testing.T) {
	installUnits(t, "orama-namespace-gateway@.service")

	if _, err := ResolveServiceName("orama-namespace-olric@anchat"); err == nil {
		t.Fatal("a namespace whose template is not installed resolved anyway")
	}
}

// The alias has to name the instance that runs today. Install writes the
// pre-migration host units too — and disables them — so an alias that prefers
// the host unit reads a journal that has had nothing in it since the migration.
func TestResolveServiceName_aliasPrefersTheNamespaceInstance(t *testing.T) {
	installUnits(t, "orama-namespace-olric@.service", "orama-olric.service")

	unit, err := ResolveServiceName("olric")
	if err != nil {
		t.Fatalf("olric: %v", err)
	}
	if unit != "orama-namespace-olric@index" {
		t.Errorf("olric resolved to %q, want the namespace instance", unit)
	}
}

func TestResolveServiceName_aliasFallsBackOnAPreMigrationNode(t *testing.T) {
	installUnits(t, "orama-olric.service")

	unit, err := ResolveServiceName("olric")
	if err != nil {
		t.Fatalf("olric: %v", err)
	}
	if unit != "orama-olric" {
		t.Errorf("olric resolved to %q on a node with no template", unit)
	}
}

func TestResolveServiceName_aliasesAreCaseInsensitive(t *testing.T) {
	installUnits(t, "orama-node.service")

	for _, alias := range []string{"node", "NODE", "Node"} {
		unit, err := ResolveServiceName(alias)
		if err != nil {
			t.Fatalf("%s: %v", alias, err)
		}
		if unit != "orama-node" {
			t.Errorf("%s resolved to %q", alias, unit)
		}
	}
}

// orama-node does not bind the gateway port and is not rqlited's parent — both
// run as their own namespace instances — so an alias resolving to orama-node
// shows a journal that has never held those logs, and shows it silently.
func TestResolveServiceName_gatewayAndRqliteAreTheirOwnUnits(t *testing.T) {
	installUnits(t,
		"orama-node.service",
		"orama-namespace-gateway@.service",
		"orama-namespace-rqlite@.service",
	)

	for alias, want := range map[string]string{
		"gateway": "orama-namespace-gateway@index",
		"rqlite":  "orama-namespace-rqlite@index",
	} {
		unit, err := ResolveServiceName(alias)
		if err != nil {
			t.Fatalf("%s: %v", alias, err)
		}
		if unit != want {
			t.Errorf("%s resolved to %q, want %q", alias, unit, want)
		}
	}
}

// A template is not a unit: systemd will not run "orama-namespace-olric@", and
// journalctl reads nothing for it, so it is refused rather than resolved to an
// empty journal.
func TestResolveServiceName_templateWithoutAnInstance(t *testing.T) {
	installUnits(t, "orama-namespace-olric@.service")

	for _, name := range []string{
		"orama-namespace-olric@",
		"orama-namespace-olric@.service",
	} {
		if unit, err := ResolveServiceName(name); err == nil {
			t.Errorf("%q resolved to %q", name, unit)
		}
	}
}

// An alias whose units are all absent has to say which units it looked for,
// or the operator has nothing to check.
func TestResolveServiceName_missingAliasNamesTheUnits(t *testing.T) {
	installUnits(t)

	_, err := ResolveServiceName("ipfs")
	if err == nil {
		t.Fatal("resolved an alias with no unit files installed")
	}
	for _, want := range []string{"orama-namespace-ipfs@index", "orama-ipfs"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %s", err, want)
		}
	}
}

func TestResolveServiceName_unknownNameListsTheAliases(t *testing.T) {
	installUnits(t)

	_, err := ResolveServiceName("postgres")
	if err == nil {
		t.Fatal("resolved a unit that is not installed")
	}
	for _, want := range []string{"node", "olric", "orama-namespace-olric@<namespace>"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
}

func TestResolveServiceName_plainUnitName(t *testing.T) {
	installUnits(t, "caddy.service")

	unit, err := ResolveServiceName("caddy.service")
	if err != nil {
		t.Fatalf("caddy.service: %v", err)
	}
	if unit != "caddy" {
		t.Errorf("resolved to %q, want the name without the suffix", unit)
	}
}

func TestResolveServiceName_timerInstance(t *testing.T) {
	installUnits(t, "orama-namespace-ipfs-gc@.timer")

	unit, err := ResolveServiceName("orama-namespace-ipfs-gc@index.timer")
	if err != nil {
		t.Fatalf("timer: %v", err)
	}
	if unit != "orama-namespace-ipfs-gc@index.timer" {
		t.Errorf("resolved to %q — a timer keeps its suffix, it is a different unit from the service", unit)
	}
}

func TestResolveServiceName_empty(t *testing.T) {
	installUnits(t, "orama-node.service")

	if _, err := ResolveServiceName(""); err == nil {
		t.Fatal("the empty string resolved to a unit")
	}
}

func TestServiceUnitExists(t *testing.T) {
	installUnits(t, "caddy.service")

	if !ServiceUnitExists("caddy") {
		t.Error("caddy.service is installed but ServiceUnitExists says no")
	}
	if ServiceUnitExists("coredns") {
		t.Error("coredns.service is not installed but ServiceUnitExists says yes")
	}
}

// The frontend units moved the same way the storage ones did: install writes
// caddy.service and coredns.service for rollback and disables them, and the
// node runs orama-namespace-caddy@index and, on a nameserver,
// orama-namespace-coredns@nameserver.
func TestResolveServiceName_frontendAliases(t *testing.T) {
	installUnits(t,
		"orama-namespace-caddy@.service", "caddy.service",
		"orama-namespace-coredns@.service", "coredns.service",
		"orama-turn.service",
	)

	for alias, want := range map[string]string{
		"caddy":   "orama-namespace-caddy@index",
		"coredns": "orama-namespace-coredns@nameserver",
		"turn":    "orama-turn",
	} {
		unit, err := ResolveServiceName(alias)
		if err != nil {
			t.Fatalf("%s: %v", alias, err)
		}
		if unit != want {
			t.Errorf("%s resolved to %q, want %q", alias, unit, want)
		}
	}
}

// TURN is one host-level unit serving every namespace (bugboard #283), so it
// has no per-namespace instance to prefer.
func TestResolveServiceName_turnHasNoInstance(t *testing.T) {
	installUnits(t, "orama-namespace-turn@.service")

	if _, err := ResolveServiceName("turn"); err == nil {
		t.Fatal("the turn alias resolved to a per-namespace instance")
	}
}

// ServiceAliases is what the CLI's help and its error messages list, so it has
// to be the whole table and not a hand-kept copy of it.
func TestServiceAliases_isTheWholeTableSorted(t *testing.T) {
	got := ServiceAliases()

	if len(got) != len(serviceAliases) {
		t.Fatalf("ServiceAliases returned %d of %d aliases: %v", len(got), len(serviceAliases), got)
	}
	for _, name := range got {
		if _, ok := serviceAliases[name]; !ok {
			t.Errorf("ServiceAliases returned %q, which is not an alias", name)
		}
	}
	for name := range serviceAliases {
		if !slices.Contains(got, name) {
			t.Errorf("ServiceAliases omits the %q alias", name)
		}
	}
	if !slices.IsSorted(got) {
		t.Errorf("ServiceAliases is unsorted: %v — the generated CLI reference would depend on map order", got)
	}
}

// The name reaches a filesystem path and a journalctl -u argument, both of
// which read more than a literal string, so it is validated at the boundary
// rather than trusted because the caller is an operator.
func TestResolveServiceName_rejectsNamesThatAreNotUnitNames(t *testing.T) {
	installUnits(t,
		"orama-namespace-olric@.service",
		"orama-node.service",
	)

	for _, name := range []string{
		// journalctl -u takes a glob, so this would read every namespace's
		// journal in one command.
		"orama-namespace-olric@*",
		"orama-namespace-olric@anchat?",
		"orama-namespace-olric@[ab]",
		// filepath.Join cleans the path, so "../" walks out of the unit
		// directory and turns the resolver into an existence oracle.
		"../../wireguard/wg0",
		"../../../root/.ssh/id_rsa",
		"etc/shadow",
		// A leading dash is a flag, not a name.
		"-f",
		"--output=cat",
		// Whitespace would split into two arguments anywhere a shell is
		// involved, and is not a unit name in any case.
		"orama-node --output=cat",
		"orama-node\nreboot",
	} {
		unit, err := ResolveServiceName(name)
		if err == nil {
			t.Errorf("%q resolved to %q", name, unit)
		}
	}
}

// Rejection has to name the input, or an operator who fat-fingered a namespace
// cannot see what they typed.
func TestResolveServiceName_rejectionQuotesTheInput(t *testing.T) {
	installUnits(t, "orama-node.service")

	_, err := ResolveServiceName("orama-namespace-olric@*")
	if err == nil {
		t.Fatal("a glob resolved")
	}
	if !strings.Contains(err.Error(), `"orama-namespace-olric@*"`) {
		t.Errorf("error %q does not quote the rejected input", err)
	}
}

// Every unit name the node actually runs has to survive the validation.
func TestResolveServiceName_acceptsTheShapesTheNodeRuns(t *testing.T) {
	installUnits(t,
		"orama-node.service",
		"orama-turn.service",
		"orama-namespace-olric@.service",
		"orama-namespace-ipfs-cluster@.service",
		"orama-namespace-ipfs-gc@.timer",
		"wg-quick@.service",
	)

	for _, name := range []string{
		"orama-node",
		"orama-node.service",
		"orama-turn",
		"orama-namespace-olric@anchat",
		"orama-namespace-olric@anchat-test-1",
		"orama-namespace-olric@anchat.service",
		"orama-namespace-ipfs-cluster@index",
		"orama-namespace-ipfs-gc@index.timer",
		"wg-quick@wg0",
	} {
		if _, err := ResolveServiceName(name); err != nil {
			t.Errorf("%s is a unit this node runs, and it was rejected: %v", name, err)
		}
	}
}

// A name beginning with a dash is refused even when a unit file by that name
// exists, so the string can never be read as a flag wherever it lands on a
// command line.
func TestResolveServiceName_rejectsALeadingDashEvenWhenInstalled(t *testing.T) {
	installUnits(t, "-f.service", "--output=cat.service")

	for _, name := range []string{"-f", "--output=cat"} {
		if unit, err := ResolveServiceName(name); err == nil {
			t.Errorf("%q resolved to %q", name, unit)
		}
	}
}
