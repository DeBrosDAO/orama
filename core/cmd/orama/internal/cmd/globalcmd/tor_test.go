package globalcmd

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/tornet"
	"github.com/spf13/cobra"
)

// tornetTestdata is the consensus and votes fixtures of pkg/tornet.
var tornetTestdata = filepath.Join("..", "..", "..", "..", "..", "pkg", "tornet", "testdata")

func TestParseAuthoritySpecs(t *testing.T) {
	specs, err := parseAuthoritySpecs([]string{"OramaAuth1=57.129.166.16", "OramaAuth2=57.129.166.17"})
	if err != nil || len(specs) != 2 || specs[0].Nickname != "OramaAuth1" || specs[0].ORPort != 31020 || specs[0].DirPort != 31021 {
		t.Fatalf("%+v %v", specs, err)
	}
	for _, bad := range []string{"", "OramaAuth1", "=1.2.3.4", "OramaAuth1="} {
		if _, err := parseAuthoritySpecs([]string{bad}); err == nil || clierr.CodeOf(err) != clierr.CodeUsage {
			t.Errorf("%q: err = %v", bad, err)
		}
	}
}

func TestReadPassphrase(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, mode os.FileMode, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	good := write("ok", 0o600, "correct horse battery staple\n")
	link := filepath.Join(dir, "link")
	if err := os.Symlink(good, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readPassphrase(link); err == nil {
		t.Error("a link to the passphrase file was accepted")
	}
	got, err := readPassphrase(good)
	if err != nil || string(got) != "correct horse battery staple" {
		t.Fatalf("%q %v", got, err)
	}
	for name, p := range map[string]string{
		"group readable": write("g", 0o640, "x"),
		"world readable": write("w", 0o644, "x"),
		"missing":        filepath.Join(dir, "absent"),
		"directory":      dir,
		"too large":      write("big", 0o600, strings.Repeat("a", passphraseFileMax+1)),
	} {
		if _, err := readPassphrase(p); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestRunCeremony_refusesBeforeRunningAnything(t *testing.T) {
	saved := ceremonyFlags
	defer func() { ceremonyFlags = saved }()
	ceremonyFlags.authorities = []string{"A=57.129.166.16", "B=57.129.166.17"}
	ceremonyFlags.name, ceremonyFlags.out, ceremonyFlags.passphrase = "orama-test", filepath.Join(t.TempDir(), "out"), filepath.Join(t.TempDir(), "absent")
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	if err := runCeremony(cmd, nil); err == nil {
		t.Fatal("a ceremony with a missing passphrase file ran")
	}
	if _, err := os.Stat(ceremonyFlags.out); err == nil {
		t.Fatal("the output directory was made")
	}
}

func TestArchiveCmd_archivesAndReportsTheSecondRunAsDone(t *testing.T) {
	data, archive := t.TempDir(), t.TempDir()
	for src, dst := range map[string]string{"consensus-microdesc.txt": tornet.DataDirConsensus, "votes.txt": tornet.DataDirVotes} {
		raw, err := os.ReadFile(filepath.Join(tornetTestdata, src))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(data, dst), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	saved := archiveFlags
	defer func() { archiveFlags = saved }()
	archiveFlags.dataDir, archiveFlags.archiveDir = data, archive
	run := func() string {
		var out bytes.Buffer
		cmd := &cobra.Command{}
		cmd.SetOut(&out)
		if err := archiveCmd.RunE(cmd, nil); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	if first := run(); !strings.HasPrefix(first, "archived: valid-after 2026-10-08 12:00:00 root ") {
		t.Errorf("first run: %s", first)
	}
	if second := run(); !strings.HasPrefix(second, "already archived: ") {
		t.Errorf("second run: %s", second)
	}
	archiveFlags.dataDir = t.TempDir()
	if err := archiveCmd.RunE(&cobra.Command{}, nil); err == nil {
		t.Error("an authority with no consensus archived something")
	}
	archiveFlags.dataDir = ""
	if err := archiveCmd.RunE(&cobra.Command{}, nil); clierr.CodeOf(err) != clierr.CodeUsage {
		t.Errorf("no --data-dir = %v", err)
	}
}

func TestPrintTorInfo(t *testing.T) {
	infos := []tornet.NodeInfo{{
		Home: "/var/lib/orama-global/tor-relay", Nickname: "Orama0123", Fingerprint: strings.Repeat("AB", 20),
		Consensus: &tornet.ConsensusInfo{Flavor: "microdesc", ValidAfter: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC), Relays: 8, Exits: 2, Listed: true, ListedFlags: []string{"Fast", "Running"}},
	}, {Home: "/var/lib/orama-global/tor-onion", Onion: strings.Repeat("a", 56) + ".onion"}}
	var text bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&text)
	if err := printTorInfo(cmd, infos, false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"fingerprint  " + strings.Repeat("AB", 20), "listed       true Fast Running", "8 relays", "consensus    none yet", ".onion"} {
		if !strings.Contains(text.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, text.String())
		}
	}

	var js bytes.Buffer
	cmd.SetOut(&js)
	if err := printTorInfo(cmd, infos, true); err != nil {
		t.Fatal(err)
	}
	var back []tornet.NodeInfo
	if err := json.Unmarshal(js.Bytes(), &back); err != nil || len(back) != 2 || !back[0].Consensus.Listed || back[1].Onion == "" {
		t.Fatalf("json = %s (%v)", js.String(), err)
	}
}

func TestTxgateCmd_listenMustBeLoopback(t *testing.T) {
	saved := txgateFlags
	defer func() { txgateFlags = saved }()
	for _, listen := range []string{"0.0.0.0:31022", ":31022", "10.0.0.1:31022", "203.0.113.5:31022", "localhost:31022", "nonsense"} {
		txgateFlags.listen, txgateFlags.upstream, txgateFlags.rate, txgateFlags.burst, txgateFlags.inFlight = listen, "http://127.0.0.1:31003", 1, 1, 1
		cmd := &cobra.Command{}
		cmd.SetContext(context.Background())
		err := txgateCmd.RunE(cmd, nil)
		if err == nil || clierr.CodeOf(err) != clierr.CodeUsage {
			t.Errorf("--listen %s: err = %v", listen, err)
		}
	}
	txgateFlags.listen, txgateFlags.upstream = "127.0.0.1:31022", "https://127.0.0.1:31003"
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	if err := txgateCmd.RunE(cmd, nil); clierr.CodeOf(err) != clierr.CodeUsage {
		t.Errorf("an https upstream: %v", err)
	}
}

func writeTorNetworkFile(t *testing.T) string {
	t.Helper()
	n := tornet.Network{Name: "orama-test", Private: true, VotingIntervalMinutes: 30, VoteDelaySeconds: 300, DistDelaySeconds: 300}
	for i, ip := range []string{"57.129.166.16", "57.129.166.17", "161.97.184.199"} {
		n.Authorities = append(n.Authorities, tornet.Authority{
			Nickname: fmt.Sprintf("OramaAuth%d", i+1), Address: ip, ORPort: 31020, DirPort: 31021,
			V3Ident: fmt.Sprintf("%040X", 0xA0+i), Fingerprint: fmt.Sprintf("%040X", 0xB0+i),
			Ed25519ID: base64.RawStdEncoding.EncodeToString(append(make([]byte, 31), byte(i+1))),
		})
	}
	body, err := n.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "tor-network.json")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestOnionsAdd_putsAValidatorOnionIntoTheNetworkFile(t *testing.T) {
	file := writeTorNetworkFile(t)
	saved := onionsFlags
	defer func() { onionsFlags = saved }()
	onionsFlags.networkFile = file
	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		cmd := &cobra.Command{}
		cmd.SetOut(&out)
		err := onionsAddCmd.RunE(cmd, args)
		return out.String(), err
	}
	out, err := run(testOnion)
	if err != nil || !strings.Contains(out, "1 validator onion services (1 added)") {
		t.Fatalf("add: %q, %v", out, err)
	}
	if out, err = run(testOnion); err != nil || !strings.Contains(out, "(0 added)") {
		t.Fatalf("adding it again: %q, %v", out, err)
	}
	n, err := tornet.Load(file)
	if err != nil || len(n.ValidatorOnions) != 1 || n.ValidatorOnions[0] != testOnion {
		t.Fatalf("file = %+v, %v", n, err)
	}
	if _, err := run("chain.example.com"); err == nil || clierr.CodeOf(err) != clierr.CodeUsage {
		t.Errorf("a clearnet host as a validator onion: %v", err)
	}
	onionsFlags.networkFile = ""
	if _, err := run(testOnion); err == nil || clierr.CodeOf(err) != clierr.CodeUsage {
		t.Errorf("no --network-file: %v", err)
	}
}

// The file `onions add` writes is the file the client side reads: a validator
// onion added by the operator is what --onion-network picks.
func TestOnionsAdd_isWhatOnionNetworkPicks(t *testing.T) {
	isolateEnv(t)
	file := writeTorNetworkFile(t)
	if _, _, err := tornet.AddValidatorOnionsToFile(file, testOnion); err != nil {
		t.Fatal(err)
	}
	_, base, done, err := chainTarget(onionCmd(t, "--onion-network", file, "--onion-tor", fakeTor(t, bootedTor)), context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	if base != "http://"+testOnion+":80" {
		t.Fatalf("base = %q", base)
	}
}

func TestMonitorCmd_writesTheRelaysMonitorFile(t *testing.T) {
	home := t.TempDir()
	saved := monitorFlags
	defer func() { monitorFlags = saved }()
	monitorFlags.home = home
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	if err := monitorCmd.RunE(cmd, nil); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(home, tornet.MonitorFile))
	if err != nil || string(body) != "{}\n" || !strings.Contains(out.String(), "unknown") {
		t.Fatalf("a relay with no consensus yet: %q, %v, %q", body, err, out.String())
	}
	monitorFlags.home = filepath.Join(home, "absent")
	if err := monitorCmd.RunE(cmd, nil); err == nil || clierr.CodeOf(err) != clierr.CodeFailure {
		t.Errorf("a missing DataDirectory: %v", err)
	}
	monitorFlags.home = ""
	if err := monitorCmd.RunE(cmd, nil); err == nil || clierr.CodeOf(err) != clierr.CodeUsage {
		t.Errorf("no --home: %v", err)
	}
}
