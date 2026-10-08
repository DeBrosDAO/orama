package tornet

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestParseConsensus_bothFlavorsGiveTheSameRelays(t *testing.T) {
	micro, err := ParseConsensus(strings.NewReader(string(readFixture(t, "consensus-microdesc.txt"))))
	if err != nil {
		t.Fatalf("microdescriptor consensus (r lines without a digest): %v", err)
	}
	full, err := ParseConsensus(strings.NewReader(string(readFixture(t, "consensus-ns.txt"))))
	if err != nil {
		t.Fatalf("full consensus (r lines with a digest): %v", err)
	}
	if micro.Flavor != "microdesc" || full.Flavor != "ns" {
		t.Fatalf("flavors %q %q", micro.Flavor, full.Flavor)
	}
	if len(micro.Relays) != 3 || len(full.Relays) != 3 {
		t.Fatalf("relays %d %d", len(micro.Relays), len(full.Relays))
	}
	for i := range micro.Relays {
		m, f := micro.Relays[i], full.Relays[i]
		if m.Nickname != f.Nickname || m.Fingerprint != f.Fingerprint || m.Address != f.Address || m.ORPort != f.ORPort || m.Bandwidth != f.Bandwidth {
			t.Errorf("relay %d differs between flavors: %+v vs %+v", i, m, f)
		}
	}
	if micro.Relays[2].Address != "161.97.184.202" || micro.Relays[2].ORPort != 31020 {
		t.Errorf("address and port are read from the end of the line: %+v", micro.Relays[2])
	}
}

func TestParseConsensus_aMalformedWeightIsAnError(t *testing.T) {
	good := string(readFixture(t, "consensus-microdesc.txt"))
	for _, bad := range []string{"Bandwidth=lots", "Bandwidth=-5", "Bandwidth="} {
		doc := strings.Replace(good, "Bandwidth=5000 Measured=5000", bad, 1)
		if _, err := ParseConsensus(strings.NewReader(doc)); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestRelayTorrc_contactCannotContinueTheLineOrOpenAQuote(t *testing.T) {
	for _, contact := range []string{`me\`, `me "x`, `me #x`} {
		c := relayConfig()
		c.Contact = contact
		if _, err := RelayTorrc(c); err == nil {
			t.Errorf("contact %q accepted", contact)
		}
	}
	c := relayConfig()
	c.Authority, c.DirPort, c.BandwidthFile = true, 31021, "relative/latest.v1"
	if _, err := RelayTorrc(c); err == nil {
		t.Error("a bandwidth file that is not an absolute plain path was accepted")
	}
}

// The DataDirectory is the Tor account's and root reads it: an onion
// "hostname" that is not an address (terminal escapes) and a link to another
// file are refused, not printed.
func TestReadNodeInfo_refusesWhatATorProcessCouldPlant(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "onion"), 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(home, "onion", "hostname"), []byte("\x1b[2J evil.onion\n"))
	if _, err := ReadNodeInfo(home, time.Now()); err == nil {
		t.Error("an onion hostname with escape sequences was printed")
	}
	other := t.TempDir()
	write(t, filepath.Join(other, "secret"), []byte("OramaAuth1 "+strings.Repeat("00", 20)))
	home = t.TempDir()
	if err := os.Symlink(filepath.Join(other, "secret"), filepath.Join(home, dataDirFingerprint)); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadNodeInfo(home, time.Now()); err == nil {
		t.Error("a link in the DataDirectory was followed")
	}
}

func TestRelayTorrc_anExitStatesWhatItNeverReaches(t *testing.T) {
	c := relayConfig()
	c.Network.AllowExit, c.Exit = true, true
	got, err := RelayTorrc(c)
	if err != nil {
		t.Fatal(err)
	}
	has(t, got, "ExitPolicyRejectPrivate 1")
	has(t, got, "ExitPolicyRejectLocalInterfaces 1")
	plain, _ := RelayTorrc(relayConfig())
	if hasPrefix(plain, "ExitPolicyReject") {
		t.Error("a relay that is not an exit carries exit options")
	}
}

// A hostile DataDirectory can plant a FIFO or a link where root reads: neither
// may hang or redirect `orama global tor info`.
func TestReadNodeInfo_aFIFOOrALinkedOnionDirectoryIsRefusedNotWaitedOn(t *testing.T) {
	home := t.TempDir()
	fifo := filepath.Join(home, DataDirConsensus)
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("no FIFOs here: %v", err)
	}
	done := make(chan error, 1)
	go func() { _, err := ReadNodeInfo(home, time.Now()); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Error("a FIFO was read as a consensus")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("reading a FIFO hung")
	}

	home = t.TempDir()
	elsewhere := t.TempDir()
	write(t, filepath.Join(elsewhere, "hostname"), []byte(strings.Repeat("a", 56)+".onion\n"))
	if err := os.Symlink(elsewhere, filepath.Join(home, onionDir)); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadNodeInfo(home, time.Now()); err == nil {
		t.Error("an onion directory that is a link was read")
	}
}

func TestReadNodeInfo_flagsThatAreNotWordsAreNotPrinted(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, dataDirFingerprint), []byte("OramaAuth1 0000 0000 0000 0000 0000 0000 0000 0000 0000 00B0\n"))
	doc := strings.Replace(string(readFixture(t, "consensus-microdesc.txt")), "s Authority Fast Guard HSDir Running Stable V2Dir Valid", "s Fast \x1b[2JEvil Running", 1)
	write(t, filepath.Join(home, DataDirConsensus), []byte(doc))
	info, err := ReadNodeInfo(home, time.Date(2026, 10, 8, 12, 10, 0, 0, time.UTC))
	if err != nil || info.Consensus == nil || !info.Consensus.Listed {
		t.Fatalf("%+v %v", info, err)
	}
	if strings.Join(info.Consensus.ListedFlags, " ") != "Fast Running" {
		t.Errorf("flags = %v", info.Consensus.ListedFlags)
	}
}

func TestReadLimited_refusesAFIFOWithoutBlocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("no FIFOs here: %v", err)
	}
	done := make(chan error, 1)
	go func() { _, err := readLimited(path); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Error("a FIFO was read")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("reading a FIFO hung")
	}
}
