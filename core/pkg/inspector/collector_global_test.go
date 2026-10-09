package inspector

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

func TestMonitorProvider_carriesTheDealSlots(t *testing.T) {
	r := monitorProvider(`{"hot_key_balance_norama":5,"held_slots":4,"pending_slots":2}`)
	if r.Error != "" || r.HeldSlots == nil || *r.HeldSlots != 4 || r.PendingSlots == nil || *r.PendingSlots != 2 {
		t.Fatalf("provider %+v", r)
	}
}

func TestMonitorProvider_emptyAndInvalidFiles(t *testing.T) {
	if r := monitorProvider(""); r.HeldSlots != nil || r.Error != "" {
		t.Fatalf("an empty file read as %+v", r)
	}
	if r := monitorProvider(`{"held_slots":-3}`); r.Error == "" {
		t.Fatal("a negative slot count was accepted")
	}
}

func TestParseGlobalCollect_peersAndHotKey(t *testing.T) {
	const stdout = `
===ORAMA_GLOBAL chain_load===
loaded
===ORAMA_GLOBAL chain_state===
active
===ORAMA_GLOBAL status===
{"jsonrpc":"2.0","id":-1,"result":{"node_info":{"id":"aa","network":"orama-stagenet-1","version":"0.39.4"},"sync_info":{"latest_block_height":"100","latest_block_time":"2026-09-28T00:00:00Z","catching_up":false},"validator_info":{"address":"0000000000000000000000000000000000000000","voting_power":"10"}}}
===ORAMA_GLOBAL net===
{"jsonrpc":"2.0","id":-1,"result":{"n_peers":"0"}}
===ORAMA_GLOBAL validators===
{"jsonrpc":"2.0","id":-1,"result":{"validators":[],"total":"3"}}
===ORAMA_GLOBAL params===
{"params":{"signed_blocks_window":"100","min_signed_per_window":"0.900000000000000000"}}
===ORAMA_GLOBAL signing===
{"info":[]}
===ORAMA_GLOBAL staking===
{"validators":[]}
===ORAMA_GLOBAL ipfs_load===
not-found
===ORAMA_GLOBAL ipfs_state===
inactive
===ORAMA_GLOBAL repo===
===ORAMA_GLOBAL provider_load===
loaded
===ORAMA_GLOBAL provider_state===
active
===ORAMA_GLOBAL provider_monitor===
{"hot_key_balance_norama":0}
===ORAMA_GLOBAL relay_load===
not-found
===ORAMA_GLOBAL relay_state===
inactive
===ORAMA_GLOBAL relay_monitor===
`
	chain, global := parseGlobalCollect(stdout, time.Date(2026, 9, 28, 0, 0, 30, 0, time.UTC))
	if chain == nil || !chain.Responsive || chain.Peers != 0 || chain.ValidatorCount != 3 {
		t.Fatalf("chain %+v", chain)
	}
	if chain.BlockAgeSec < 29 || chain.BlockAgeSec > 31 {
		t.Fatalf("block age %v", chain.BlockAgeSec)
	}
	if chain.SigningError == "" {
		t.Fatal("a validator missing from staking was treated as found")
	}
	if global == nil || global.Provider == nil || global.Provider.HotKeyBalanceNorama == nil || *global.Provider.HotKeyBalanceNorama != 0 {
		t.Fatalf("global %+v", global)
	}
}

func TestParseGlobalCollect_directoryAuthorityMonitor(t *testing.T) {
	const stdout = `
===ORAMA_GLOBAL provider_load===
not-found
===ORAMA_GLOBAL relay_load===
not-found
===ORAMA_GLOBAL relay_monitor===
{"in_consensus":true}
===ORAMA_GLOBAL dirauth_load===
loaded
===ORAMA_GLOBAL dirauth_state===
active
===ORAMA_GLOBAL dirauth_monitor===
{"in_consensus":false}
`
	_, global := parseGlobalCollect(stdout, time.Now())
	if global == nil || global.Relay == nil || global.Relay.InConsensus == nil || *global.Relay.InConsensus {
		t.Fatalf("global %+v: the authority's file says it is not in the consensus", global)
	}
	if len(global.Units) != 1 || global.Units[0].Name != constants.GlobalTorDirauthUnit || global.Units[0].State != "active" {
		t.Fatalf("units %+v", global.Units)
	}
	for _, bad := range []string{"", "not json", `{"in_consensus":1}`} {
		_, g := parseGlobalCollect(strings.Replace(stdout, `{"in_consensus":false}`, bad, 1), time.Now())
		if g == nil || g.Relay == nil || (bad != "" && g.Relay.Error == "") || g.Relay.InConsensus != nil {
			t.Errorf("monitor %q read as %+v", bad, g)
		}
	}
}

// A role's account owns its home and can replace monitor.json with a link or a
// FIFO; the reader is root over sudo, so none of the three monitor reads in the
// script may follow a link or wait on a FIFO, as `head` and `cat` do.
func TestGlobalCollectScript_monitorReadsRefuseLinksAndFIFOs(t *testing.T) {
	script := globalCollectScript()
	homes := []string{constants.GlobalProviderHome, constants.GlobalTorRelayHome, constants.GlobalTorDirauthHome}
	for _, home := range homes {
		want := monitorRead(home)
		if !strings.Contains(script, "\n"+want+"\n") {
			t.Errorf("the script does not read %s with %q", home, want)
		}
	}
	for _, line := range strings.Split(script, "\n") {
		if strings.Contains(line, constants.GlobalMonitorFile) && !strings.Contains(line, "iflag=nofollow,nonblock") {
			t.Errorf("a monitor file is read without nofollow: %q", line)
		}
	}
}

// monitorRead behaves as it says: a regular file is read, a link to a file only
// root can read gives nothing, and a FIFO is not waited on. dd needs GNU's
// iflag, which the BSD one lacks, so the test says so and stops there.
func TestMonitorRead_behaviour(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	dir := t.TempDir()
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("ROOT-ONLY"), 0o600); err != nil {
		t.Fatal(err)
	}
	probe := exec.Command(bash, "-c", "ln -s "+secret+" "+filepath.Join(dir, "probe")+" && dd if="+filepath.Join(dir, "probe")+" bs=16 count=1 iflag=nofollow 2>&1 | head -c 200")
	if out, _ := probe.CombinedOutput(); strings.Contains(string(out), "ROOT-ONLY") || strings.Contains(string(out), "invalid") || strings.Contains(string(out), "unrecognized") {
		t.Skipf("this dd has no working iflag=nofollow: %s", out)
	}
	read := func(home string) string {
		// sudo -n is the host's; here the test is its own user.
		line := strings.Replace(monitorRead(home), "sudo -n ", "", 1)
		out, _ := exec.Command(bash, "-c", line).Output()
		return string(out)
	}
	if err := os.WriteFile(filepath.Join(dir, constants.GlobalMonitorFile), []byte(`{"in_consensus":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := read(dir); got != `{"in_consensus":true}` {
		t.Errorf("a regular file read as %q", got)
	}

	link := t.TempDir()
	if err := os.Symlink(secret, filepath.Join(link, constants.GlobalMonitorFile)); err != nil {
		t.Fatal(err)
	}
	if got := read(link); got != "" {
		t.Errorf("a link to a file the account cannot read was followed: %q", got)
	}

	fifo := t.TempDir()
	if err := exec.Command("mkfifo", filepath.Join(fifo, constants.GlobalMonitorFile)).Run(); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	done := make(chan string, 1)
	go func() { done <- read(fifo) }()
	select {
	case got := <-done:
		if got != "" {
			t.Errorf("a FIFO read as %q", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the read waited on a FIFO")
	}
}
