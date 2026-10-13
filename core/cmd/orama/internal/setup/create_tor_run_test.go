package setup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/clusterreg"
	"github.com/DeBrosOfficial/network/pkg/netregistry"
	"github.com/DeBrosOfficial/network/pkg/tornet"
)

// torOpts is a creation of "stagenet" with n directory authorities on the first n machines.
func (h *createHarness) torOpts(n int, ips ...string) Options {
	o := h.createOpts(ips...)
	o.Create.TorAuthorities, o.Create.TorAuthoritiesSet = n, true
	h.seats.tor.authorities = n
	return o
}

// finished makes the machines look to the next run as a run that got past the chains left
// them: the chain home with its genesis, the units installed and running.
func (h *createHarness) finished(ips []string, genesis []byte) {
	h.seats.order = nil // the machines are enrolled again, in the same order, with the same keys
	h.enroll.facts = map[string]Facts{}
	for _, ip := range ips {
		h.seats.carries(ip, genesis, true)
		h.enroll.facts[ip] = Facts{Arch: "amd64", Hardware: goodHardware(), ClusterInstalled: true, GlobalInstalled: true, ChainActive: true, ManifestSHA256: testManifest, CLISHA256: testCLISHA}
	}
}

// torLog is the entries of the log from index from on that are about a Tor role.
func torLog(w *world, from int) []string {
	var out []string
	for _, e := range w.entries()[from:] {
		if strings.HasPrefix(e, "tor-") {
			out = append(out, e)
		}
	}
	return out
}

func countPrefix(entries []string, prefix string) int {
	n := 0
	for _, e := range entries {
		if strings.HasPrefix(e, prefix) {
			n++
		}
	}
	return n
}

func indexOf(entries []string, prefix string) int {
	return slices.IndexFunc(entries, func(e string) bool { return strings.HasPrefix(e, prefix) })
}

func publishedTor(t *testing.T, h *createHarness) (netregistry.Manifest, []byte, tornet.Network) {
	t.Helper()
	dir := filepath.Join(h.publishDir, "stagenet")
	raw, err := os.ReadFile(filepath.Join(dir, netregistry.ManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	m, err := netregistry.ParseManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.ReadFile(filepath.Join(dir, netregistry.TorNetworkFile))
	if err != nil {
		t.Fatalf("the creation published no tor-network.json: %v", err)
	}
	n, err := tornet.ParseNetwork(file)
	if err != nil {
		t.Fatalf("the published tor-network.json does not load: %v", err)
	}
	return *m, file, n
}

func TestRunCreate_tor_authoritiesFirstThenRelaysAndTheBootstrapIsLeftOneAtATime(t *testing.T) {
	h := newCreateHarness(t)
	res := h.mustCreate(t, h.torOpts(3, torIPs...))
	entries := torLog(h.w, 0)
	all := h.w.entries()

	// The Tor step comes after the chains run and the nodes are registered.
	if indexOf(entries, "tor-keys ") < 0 || h.w.index("tor-keys ") < h.w.index("tx register-node ") || h.w.index("tor-keys ") < h.w.index("startglobal "+torIPs[4]) {
		t.Errorf("the Tor step must come after the chains are started and the nodes registered:\n%s", strings.Join(all, "\n"))
	}
	// Keys: made on the three authority machines only, before anything is installed.
	if got := countPrefix(entries, "tor-keys "); got != 3 {
		t.Errorf("%d authority key sets made, want 3: %v", got, entries)
	}
	for _, ip := range torIPs[3:] {
		if indexOf(entries, "tor-keys "+ip) >= 0 {
			t.Errorf("%s is a relay and got authority keys", ip)
		}
	}
	firstInstall := indexOf(entries, "tor-install ")
	for _, ip := range torIPs[:3] {
		if i := indexOf(entries, "tor-keys "+ip); i < 0 || i > firstInstall {
			t.Errorf("the keys of %s were not made before the first install: %v", ip, entries)
		}
	}

	// Installed with the bootstrap file first: authorities with their own bundle, relays with their node's name.
	for i, ip := range torIPs[:3] {
		want := fmt.Sprintf("tor-install %s dirauth bootstrap=true bundle=%q node=\"\"", ip, "/var/lib/orama-tor-authority/deploy/"+torAuthorityNickname(i))
		if !slices.Contains(entries, want) {
			t.Errorf("missing %q in %v", want, entries)
		}
	}
	for i, ip := range torIPs[3:] {
		want := fmt.Sprintf("tor-install %s relay bootstrap=true bundle=\"\" node=%q", ip, NodeNames(DefaultCreateNodeName, 5)[3+i])
		if !slices.Contains(entries, want) {
			t.Errorf("missing %q in %v", want, entries)
		}
	}
	// Started: all installs, then the authorities one after another, then the relays.
	lastBootstrapInstall := -1
	for i, e := range entries {
		if strings.HasPrefix(e, "tor-install ") && strings.Contains(e, "bootstrap=true") {
			lastBootstrapInstall = i
		}
	}
	prev := lastBootstrapInstall
	for _, ip := range torIPs {
		i := indexOf(entries, "tor-start "+ip)
		if i < prev {
			t.Fatalf("%s was started before the previous machine, or before the installs finished: %v", ip, entries)
		}
		prev = i
	}
	// Then the bootstrap mode is left: each machine's final file is installed before its restart.
	firstRestart := indexOf(entries, "tor-restart ")
	if firstRestart < prev {
		t.Errorf("a restart came before the last start: %v", entries)
	}
	prev = firstRestart
	for i, ip := range torIPs {
		install := slices.IndexFunc(entries, func(e string) bool {
			return strings.HasPrefix(e, "tor-install "+ip) && strings.Contains(e, "bootstrap=false")
		})
		restart := indexOf(entries, "tor-restart "+ip)
		if install < 0 || restart < install {
			t.Errorf("%s: final install at %d, restart at %d: %v", ip, install, restart, entries)
		}
		// The authorities go one at a time in the order of the machines, and the relays after the last of them.
		if i < 3 && restart < prev {
			t.Errorf("the authority %s was restarted before the previous one: %v", ip, entries)
		}
		if i >= 3 && restart < indexOf(entries, "tor-restart "+torIPs[2]) {
			t.Errorf("the relay %s was restarted before the authorities were done: %v", ip, entries)
		}
		prev = restart
	}
	// The rule the machines enforce, kept: authorities restart at least 30 minutes apart.
	var authRestarts []time.Time
	for _, r := range h.seats.tor.restarts {
		if slices.Contains(torIPs[:3], r.ip) {
			authRestarts = append(authRestarts, r.at)
		}
	}
	if len(authRestarts) != 3 {
		t.Fatalf("%d authority restarts, want 3", len(authRestarts))
	}
	for i := 1; i < len(authRestarts); i++ {
		if gap := authRestarts[i].Sub(authRestarts[i-1]); gap < tornet.AuthorityLearnWindow {
			t.Errorf("authorities restarted %s apart, want at least %s", gap, tornet.AuthorityLearnWindow)
		}
	}
	checkTorPublished(t, h, res)
}

func checkTorPublished(t *testing.T, h *createHarness, res *Result) {
	t.Helper()
	m, file, n := publishedTor(t, h)
	if m.TorNetworkSHA256 != netregistry.Digest(file) {
		t.Errorf("the manifest pins %s, the file published is %s", m.TorNetworkSHA256, netregistry.Digest(file))
	}
	if res.Created == nil || res.Created.Manifest.TorNetworkSHA256 != m.TorNetworkSHA256 {
		t.Errorf("Result.Created pins %+v, want %s", res.Created, m.TorNetworkSHA256)
	}
	if n.Bootstrap || !n.Private || n.Name != "stagenet" || n.VotingIntervalMinutes != 30 || len(n.ValidatorOnions) != 0 {
		t.Errorf("the published network = %+v: it is the final file, without the bootstrap mode", n)
	}
	for ip, m := range h.seats.tor.machines {
		if m.sha != netregistry.Digest(file) || !m.active {
			t.Errorf("%s ends with network file %s (active %v), want the published one", ip, m.sha, m.active)
		}
	}
	if err := m.VerifyTorNetwork(file); err != nil {
		t.Error(err)
	}
}

func TestRunCreate_tor_theChainLayerCarriesNoTorRoleAndTheNodesRegisterTheirRoles(t *testing.T) {
	h := newCreateHarness(t)
	h.mustCreate(t, h.torOpts(3, torIPs...))
	for _, ip := range torIPs {
		names := h.seats.seats[ip].initIn.Node.ServiceNames()
		for _, bad := range []string{"relay", "dirauth", "exit"} {
			if slices.Contains(names, bad) {
				t.Errorf("%s: the chain install names %q: %v (Tor roles are installed after the chain runs)", ip, bad, names)
			}
		}
		if len(h.seats.seats[ip].initIn.TorNetwork) != 0 {
			t.Errorf("%s: the chain install carries a Tor network file", ip)
		}
	}
	names := NodeNames(DefaultCreateNodeName, 5)
	for i, name := range names {
		roles := []int{clusterreg.RoleStorage}
		if i >= 3 {
			roles = append(roles, clusterreg.RoleRelay)
		}
		if want := fmt.Sprintf("tx register-node %s roles=%v ", name, roles); h.w.index(want) < 0 {
			t.Errorf("missing %q (an authority registers storage only, a relay storage and relay):\n%s", want, strings.Join(h.w.entries(), "\n"))
		}
	}
}

func TestRunCreate_tor_threeMachinesAreThreeAuthoritiesAndNoRelay(t *testing.T) {
	h := newCreateHarness(t)
	res := h.mustCreate(t, h.torOpts(3, torIPs[:3]...))
	entries := torLog(h.w, 0)
	if countPrefix(entries, "tor-install ") != 6 || strings.Contains(strings.Join(entries, "\n"), " relay ") {
		t.Errorf("three authorities install twice each and no relay exists: %v", entries)
	}
	checkTorPublished(t, h, res)
}

func TestRunCreate_tor_exitRelaysAreExitsAndAuthoritiesNeverAre(t *testing.T) {
	h := newCreateHarness(t)
	opts := h.torOpts(3, torIPs...)
	opts.Exit = true
	h.mustCreate(t, opts)
	entries := torLog(h.w, 0)
	for _, ip := range torIPs[:3] {
		if slices.ContainsFunc(entries, func(e string) bool { return strings.HasPrefix(e, "tor-install "+ip) && strings.Contains(e, " exit ") }) {
			t.Errorf("the authority %s was installed as an exit: %v", ip, entries)
		}
	}
	for _, ip := range torIPs[3:] {
		if !slices.ContainsFunc(entries, func(e string) bool { return strings.HasPrefix(e, "tor-install "+ip) && strings.Contains(e, " exit ") }) {
			t.Errorf("the relay %s was not installed as an exit: %v", ip, entries)
		}
	}
	_, _, n := publishedTor(t, h)
	if !n.AllowExit {
		t.Error("the network file does not allow exits, so the exit install would be refused")
	}
	if h.w.index(fmt.Sprintf("tx register-node %s roles=%v ", NodeNames(DefaultCreateNodeName, 5)[4], []int{clusterreg.RoleStorage, clusterreg.RoleRelay, clusterreg.RoleExit})) < 0 {
		t.Errorf("an exit registers the relay and exit roles:\n%s", strings.Join(h.w.entries(), "\n"))
	}
}

// Circuits are built over the relays: the authorities' /16 networks do not count, unless
// there are no relays and the authorities are all there is.
func TestRunCreate_tor_sharedSubnetsOnlyWhenTheRelaysAreInFewerThanThree(t *testing.T) {
	cases := []struct {
		name   string
		ips    []string
		shared bool
	}{
		{"two relays in one /16", torIPs, true},
		// Three authorities in three /16 networks do not help: the two relays are in two.
		{"authorities spread, relays in two", []string{"57.129.166.16", "161.97.184.199", "141.94.12.3", "51.75.64.8", "51.75.65.9"}, true},
		{"three relays in three /16 networks", []string{"57.129.166.16", "57.129.166.17", "161.97.184.199", "141.94.12.3", "51.75.64.8", "146.59.1.1"}, false},
		{"no relay, the authorities in three /16 networks", []string{"57.129.166.16", "161.97.184.199", "141.94.12.3"}, false},
		{"no relay, the authorities in two", []string{"57.129.166.16", "57.129.166.17", "161.97.184.199"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newCreateHarness(t)
			res := h.mustCreate(t, h.torOpts(3, c.ips...))
			if said := strings.Contains(strings.Join(res.Created.NextSteps(), "\n"), "allow_shared_subnets"); said != c.shared {
				t.Errorf("the next steps tell the maintainer of the allowance: %v, want %v", said, c.shared)
			}
			if _, _, n := publishedTor(t, h); n.AllowSharedSubnets != c.shared {
				t.Errorf("allow_shared_subnets = %v, want %v", n.AllowSharedSubnets, c.shared)
			}
			if said := strings.Contains(h.report.text(), "allow_shared_subnets"); said != c.shared {
				t.Errorf("the operator was told of the allowance: %v, want %v", said, c.shared)
			}
		})
	}
}

func TestRunCreate_tor_theOperatorIsToldWhatItWaitsFor(t *testing.T) {
	h := newCreateHarness(t)
	h.mustCreate(t, h.torOpts(3, torIPs...))
	text := h.report.text()
	for _, want := range []string{
		"Tor authority OramaAuth01 " + torIPs[0], "waiting for the network's first Tor consensus", "Running vote for 30m0s",
		"leaving the bootstrap mode: 3 authority(ies), one at a time, then 2 relay(s) together", "the Tor network stagenet is up: 3 directory authorities and 2 relays",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the progress output lacks %q:\n%s", want, text)
		}
	}
	for _, ip := range torIPs {
		if !h.report.has(ip, StepTor, StateDone) {
			t.Errorf("%s has no finished Tor step", ip)
		}
	}
}

func TestRunCreate_tor_optingOutTouchesNoTorRoleAndPinsNothing(t *testing.T) {
	h := newCreateHarness(t)
	res := h.mustCreate(t, h.createOpts(torIPs...))
	if entries := torLog(h.w, 0); len(entries) != 0 {
		t.Errorf("a creation with --tor-authorities 0 did Tor work: %v", entries)
	}
	if res.Created.Manifest.TorNetworkSHA256 != "" {
		t.Errorf("the manifest pins a Tor network the run did not make: %s", res.Created.Manifest.TorNetworkSHA256)
	}
	if _, err := os.Stat(filepath.Join(h.publishDir, "stagenet", netregistry.TorNetworkFile)); err == nil {
		t.Error("a tor-network.json was published")
	}
}

func TestRunCreate_tor_aRunAgainOnFinishedMachinesChangesNothing(t *testing.T) {
	h := newCreateHarness(t)
	h.mustCreate(t, h.torOpts(3, torIPs...))
	genesis := h.seats.seats[torIPs[0]].genesis
	h.finished(torIPs, genesis)
	mark := len(h.w.entries())
	res := h.mustCreate(t, h.torOpts(3, torIPs...))
	entries := torLog(h.w, mark)
	for _, prefix := range []string{"tor-install ", "tor-start ", "tor-restart "} {
		if n := countPrefix(entries, prefix); n != 0 {
			t.Errorf("a run on machines that are done did %q %d times: %v", prefix, n, entries)
		}
	}
	if slices.ContainsFunc(entries, func(e string) bool { return strings.Contains(e, "created=true") }) {
		t.Errorf("the authorities' keys were made again: %v", entries)
	}
	if got := countPrefix(entries, "tor-keys "); got != 3 {
		t.Errorf("the identities are read back from the 3 authorities: %v", entries)
	}
	checkTorPublished(t, h, res)
}

func TestRunCreate_tor_aRunThatStoppedWhileLeavingTheBootstrapContinuesWhereItStopped(t *testing.T) {
	h := newCreateHarness(t)
	h.seats.tor.failRestart[torIPs[1]] = errBoom
	_, err := Run(context.Background(), h.torOpts(3, torIPs...), h.deps)
	if err == nil || !strings.Contains(err.Error(), torIPs[1]) || !errors.Is(err, errBoom) {
		t.Fatalf("err = %v, want the second authority's restart failure", err)
	}
	if got := countPrefix(torLog(h.w, 0), "tor-restart "+torIPs[2]); got != 0 {
		t.Errorf("the third authority was restarted after the second failed: the run goes one at a time")
	}
	delete(h.seats.tor.failRestart, torIPs[1])
	genesis := h.seats.seats[torIPs[0]].genesis
	h.finished(torIPs, genesis)
	mark := len(h.w.entries())
	res := h.mustCreate(t, h.torOpts(3, torIPs...))
	entries := torLog(h.w, mark)
	if slices.ContainsFunc(entries, func(e string) bool {
		return strings.HasPrefix(e, "tor-install "+torIPs[0]) || strings.HasPrefix(e, "tor-restart "+torIPs[0])
	}) {
		t.Errorf("the first authority was done and was touched again: %v", entries)
	}
	if got := countPrefix(entries, "tor-restart "); got != 4 {
		t.Errorf("%d restarts, want the remaining 4 machines: %v", got, entries)
	}
	if slices.ContainsFunc(entries, func(e string) bool { return strings.Contains(e, "created=true") }) {
		t.Errorf("the keys were made again: %v", entries)
	}
	checkTorPublished(t, h, res)
}

func TestRunCreate_tor_aFinalFileInstalledButNotYetRunningIsRestartedWithoutInstallingAgain(t *testing.T) {
	h := newCreateHarness(t)
	h.seats.tor.failRestart[torIPs[0]] = errBoom
	if _, err := Run(context.Background(), h.torOpts(3, torIPs...), h.deps); !errors.Is(err, errBoom) {
		t.Fatalf("err = %v", err)
	}
	installs := countPrefix(torLog(h.w, 0), "tor-install "+torIPs[0])
	delete(h.seats.tor.failRestart, torIPs[0])
	if !h.seats.tor.machines[torIPs[0]].restartPending {
		t.Fatal("the simulated machine should have a torrc newer than its process")
	}
	h.finished(torIPs, h.seats.seats[torIPs[0]].genesis)
	mark := len(h.w.entries())
	h.mustCreate(t, h.torOpts(3, torIPs...))
	entries := torLog(h.w, mark)
	if got := countPrefix(entries, "tor-install "+torIPs[0]); got != 0 {
		t.Errorf("the final file was installed again (%d installs before): %v", installs, entries)
	}
	if indexOf(entries, "tor-restart "+torIPs[0]) < 0 {
		t.Errorf("the authority still ran the bootstrap configuration and was not restarted: %v", entries)
	}
}

func TestRunCreate_tor_aMachineWithAnotherNetworkFileIsRefusedBeforeAnythingIsInstalled(t *testing.T) {
	h := newCreateHarness(t)
	other := h.seats.tor.machine(torIPs[4])
	other.role, other.installed, other.sha = TorRelay, true, strings.Repeat("e", 64)
	_, err := Run(context.Background(), h.torOpts(3, torIPs...), h.deps)
	if err == nil || !strings.Contains(err.Error(), "did not make") || !strings.Contains(err.Error(), torIPs[4]) {
		t.Fatalf("err = %v, want the foreign network file named", err)
	}
	if n := countPrefix(torLog(h.w, 0), "tor-install "); n != 0 {
		t.Errorf("%d installs happened before the refusal", n)
	}
	// Nothing was pinned: the manifest and the published directory say nothing of a Tor
	// network no machine runs.
	if _, err := os.Stat(filepath.Join(h.publishDir, "stagenet", netregistry.TorNetworkFile)); err == nil {
		t.Error("a tor-network.json was published although a machine refused it")
	}
	raw, err := os.ReadFile(filepath.Join(h.publishDir, "stagenet", netregistry.ManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	if m, err := netregistry.ParseManifest(raw); err != nil || m.TorNetworkSHA256 != "" {
		t.Errorf("the manifest pins %q: %v", m.TorNetworkSHA256, err)
	}
}

// A run again with other flags (--exit changes the network file and gives the relays the exit
// role) is refused, and leaves the manifest pinning the file the machines run. The relays were
// registered without the exit role, so the on-chain step refuses first (missingRoles); without
// that, the machines' network file would be refused before anything is published.
func TestRunCreate_tor_aRunWithOtherFlagsKeepsThePinOfTheFileTheMachinesRun(t *testing.T) {
	h := newCreateHarness(t)
	h.mustCreate(t, h.torOpts(3, torIPs...))
	manifest, file, _ := publishedTor(t, h)
	genesis := h.seats.seats[torIPs[0]].genesis
	h.finished(torIPs, genesis)
	again := h.torOpts(3, torIPs...)
	again.Exit = true
	if _, err := Run(context.Background(), again, h.deps); err == nil || !strings.Contains(err.Error(), "ROLE_EXIT") {
		t.Fatalf("err = %v, want the exit role the relays lack named", err)
	}
	after, afterFile, _ := publishedTor(t, h)
	if after.TorNetworkSHA256 != manifest.TorNetworkSHA256 || string(afterFile) != string(file) {
		t.Errorf("the pin moved to a file no machine runs: %s -> %s", manifest.TorNetworkSHA256, after.TorNetworkSHA256)
	}
}

func TestRunCreate_tor_failuresStopTheRunWhereTheyHappen(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*torSim)
		want  string
		// absent is a log prefix that must not appear.
		absent string
	}{
		{"an install", func(s *torSim) { s.failInstall[torIPs[3]] = errBoom }, "install the Tor relay", "tor-start "},
		{"a start", func(s *torSim) { s.failStart[torIPs[1]] = errBoom }, "boom", "tor-restart "},
		{"a restart that is not the authority refusal", func(s *torSim) { s.failRestart[torIPs[0]] = errors.New("unit failed to start") }, "unit failed to start", "tor-restart "},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newCreateHarness(t)
			c.setup(h.seats.tor)
			_, err := Run(context.Background(), h.torOpts(3, torIPs...), h.deps)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
			if n := countPrefix(torLog(h.w, 0), c.absent); n != 0 {
				t.Errorf("%d %q entries after the failure: %v", n, c.absent, torLog(h.w, 0))
			}
		})
	}
}

func TestRunCreate_tor_aConsensusThatNeverListsAMachineEndsTheWaitAndNamesIt(t *testing.T) {
	h := newCreateHarness(t)
	h.deps.Timing.TorConsensusDeadline = 300 * time.Millisecond
	h.seats.tor.neverListed[torIPs[4]] = true
	_, err := Run(context.Background(), h.torOpts(3, torIPs...), h.deps)
	if err == nil || !strings.Contains(err.Error(), "gave up waiting") || !strings.Contains(err.Error(), torIPs[4]+" (relay): the consensus does not list it") {
		t.Fatalf("err = %v, want the wait to end naming the relay that is not listed", err)
	}
	if n := countPrefix(torLog(h.w, 0), "tor-restart "); n != 0 {
		t.Errorf("the bootstrap mode was left before the network worked: %d restarts", n)
	}
}

// The Tor network takes hours and nothing else waits for it: the domain wait and the record
// of the operator come before it, so a failure in it leaves them done and still returns the
// result the caller prints the next steps from.
func TestRunCreate_tor_comesAfterTheDomainAndTheRecordAndAFailureStillReturnsWhatWasCreated(t *testing.T) {
	h := newCreateHarness(t)
	h.deps.Domain = fakeDomain{w: h.w}
	h.seats.tor.failStart[torIPs[1]] = errBoom
	opts := h.torOpts(3, torIPs...)
	opts.Domain = "stagenet.orama.network"
	res, err := Run(context.Background(), opts, h.deps)
	if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), "its Tor network is not finished") {
		t.Fatalf("err = %v, want the Tor failure saying the network itself is created", err)
	}
	if res == nil || res.Created == nil || len(res.Created.NextSteps()) == 0 {
		t.Fatalf("result = %+v: a Tor failure must still return what was created", res)
	}
	keys := h.w.index("tor-keys ")
	for _, before := range []string{"domain wait", "record operator "} {
		if i := h.w.index(before); i < 0 || i > keys {
			t.Errorf("%q at %d, the Tor step at %d:\n%s", before, i, keys, strings.Join(h.w.entries(), "\n"))
		}
	}
}

// With only authorities (no relay restarts), the last wait still asks every machine for a
// consensus made after the last restart: an authority that lags holds an older one.
func TestRunCreate_tor_theLastWaitHoldsEveryMachineToTheLatestAuthorityRestartWhenThereAreNoRelays(t *testing.T) {
	h := newCreateHarness(t)
	h.seats.tor.lag[torIPs[0]] = 45 * time.Minute
	h.mustCreate(t, h.torOpts(3, torIPs[:3]...))
	text := h.report.text()
	i := strings.Index(text, "still waiting for every machine to be listed in a consensus of the final Tor network file")
	if i < 0 || !strings.Contains(text[i:], torIPs[0]+" (dirauth): it still holds the consensus valid after") {
		t.Errorf("the last wait did not hold the lagging authority to a consensus made after the last restart:\n%s", text)
	}
}

func TestRunCreate_tor_aRelayListedWithoutRunningIsNotUsableAndEndsTheWait(t *testing.T) {
	h := newCreateHarness(t)
	h.deps.Timing.TorConsensusDeadline = 300 * time.Millisecond
	h.seats.tor.notRunning[torIPs[3]] = true
	_, err := Run(context.Background(), h.torOpts(3, torIPs...), h.deps)
	if err == nil || !strings.Contains(err.Error(), torIPs[3]+" (relay): the consensus lists it without the Running flag") {
		t.Fatalf("err = %v, want the relay that is listed without Running named", err)
	}
	if n := countPrefix(torLog(h.w, 0), "tor-restart "); n != 0 {
		t.Errorf("the bootstrap mode was left with a relay no client would use: %d restarts", n)
	}
}

func TestRunCreate_tor_aMachineThatAnswersForAnotherAuthorityIsRefused(t *testing.T) {
	h := newCreateHarness(t)
	h.deps.Enroll = enrollerFunc(func(ctx context.Context, req MachineRequest) (Machine, error) {
		m, err := h.seats.Enroll(ctx, req)
		if err == nil {
			m = liar{m.(*seatFake)}
		}
		return m, err
	})
	_, err := Run(context.Background(), h.torOpts(3, torIPs...), h.deps)
	if err == nil || !strings.Contains(err.Error(), "answered with the authority") {
		t.Fatalf("err = %v", err)
	}
	if n := countPrefix(torLog(h.w, 0), "tor-install "); n != 0 {
		t.Errorf("%d installs after a machine lied about its identity", n)
	}
}

func TestRunCreate_tor_keysWhoseBundleIsElsewhereAreRefusedBeforeAnythingIsInstalled(t *testing.T) {
	h := newCreateHarness(t)
	h.deps.Enroll = enrollerFunc(func(ctx context.Context, req MachineRequest) (Machine, error) {
		m, err := h.seats.Enroll(ctx, req)
		if err == nil {
			m = elsewhere{m.(*seatFake)}
		}
		return m, err
	})
	_, err := Run(context.Background(), h.torOpts(3, torIPs...), h.deps)
	if err == nil || !strings.Contains(err.Error(), "makes it at") {
		t.Fatalf("err = %v", err)
	}
	if n := countPrefix(torLog(h.w, 0), "tor-install "); n != 0 {
		t.Errorf("%d installs after a machine named a bundle it was not asked to make", n)
	}
}

// elsewhere is a seat whose authority keys say the bundle is outside the keys home.
type elsewhere struct{ *seatFake }

func (e elsewhere) AuthorityKeys(ctx context.Context, in AuthorityKeysInput) (tornet.AuthorityKeys, error) {
	keys, err := e.seatFake.AuthorityKeys(ctx, in)
	keys.Bundle = "/tmp/planted/" + in.Nickname
	return keys, err
}

// liar is a seat whose authority keys are for another authority.
type liar struct{ *seatFake }

func (l liar) AuthorityKeys(ctx context.Context, in AuthorityKeysInput) (tornet.AuthorityKeys, error) {
	in.Nickname = "OramaAuth99"
	return l.seatFake.AuthorityKeys(ctx, in)
}

func TestRunCreate_tor_aConnectionThatCannotRunARoleIsFoundBeforeAnythingIsInstalled(t *testing.T) {
	h := newCreateHarness(t)
	h.deps.Enroll = enrollerFunc(func(ctx context.Context, req MachineRequest) (Machine, error) {
		m, err := h.seats.Enroll(ctx, req)
		if err == nil {
			m = noTor{m.(Bootstrapper)}
		}
		return m, err
	})
	_, err := Run(context.Background(), h.torOpts(3, torIPs...), h.deps)
	if err == nil || !strings.Contains(err.Error(), "cannot run a Tor role") {
		t.Fatalf("err = %v", err)
	}
	if h.w.count("cluster ") != 0 {
		t.Errorf("a machine was installed before the run found it cannot take its role")
	}
}

// noTor is a seat that is a Bootstrapper and not a TorHost: embedding the interface
// leaves the Tor methods out of its method set.
type noTor struct{ Bootstrapper }
