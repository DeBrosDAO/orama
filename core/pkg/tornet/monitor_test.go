package tornet

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// relayHome is a DataDirectory holding a relay identity and a consensus.
func relayHome(t *testing.T, fingerprint string, withConsensus bool) string {
	t.Helper()
	home := t.TempDir()
	if fingerprint != "" {
		line := "OramaRelayTest " + fingerprint + "\n"
		if err := os.WriteFile(filepath.Join(home, dataDirFingerprint), []byte(line), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if withConsensus {
		raw, err := os.ReadFile(filepath.Join("testdata", "consensus-microdesc.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, dataDirMicrodescConsens), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func readMonitor(t *testing.T, home string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(home, MonitorFile))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// listedFingerprint is the first relay of the microdescriptor fixture.
func listedFingerprint(t *testing.T) (string, time.Time) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "consensus-microdesc.txt"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := ParseConsensus(bytes.NewReader(raw))
	if err != nil || len(c.Relays) == 0 {
		t.Fatalf("fixture: %v", err)
	}
	return c.Relays[0].Fingerprint, c.ValidAfter.Add(time.Minute)
}

func TestWriteMonitor_listedRelay(t *testing.T) {
	fp, now := listedFingerprint(t)
	home := relayHome(t, fp, true)
	got, err := WriteMonitor(home, now)
	if err != nil || got == nil || !*got {
		t.Fatalf("listed relay: %v, %v", got, err)
	}
	if body := readMonitor(t, home); body != "{\"in_consensus\":true}\n" {
		t.Fatalf("monitor.json = %q", body)
	}
	if info, _ := os.Stat(filepath.Join(home, MonitorFile)); info.Mode().Perm() != monitorMode {
		t.Errorf("mode %o", info.Mode().Perm())
	}
}

func TestWriteMonitor_relayTheConsensusDoesNotList(t *testing.T) {
	_, now := listedFingerprint(t)
	home := relayHome(t, "00000000000000000000000000000000000000EE", true)
	got, err := WriteMonitor(home, now)
	if err != nil || got == nil || *got {
		t.Fatalf("unlisted relay: %v, %v", got, err)
	}
	if body := readMonitor(t, home); body != "{\"in_consensus\":false}\n" {
		t.Fatalf("monitor.json = %q", body)
	}
}

// What the relay cannot know it does not say: no consensus yet, no identity
// yet, or a consensus past its validity all leave the field out, and a stale
// earlier answer is replaced.
func TestWriteMonitor_unknownLeavesTheFieldOut(t *testing.T) {
	fp, now := listedFingerprint(t)
	cases := map[string]struct {
		home string
		now  time.Time
	}{
		"no consensus yet":     {relayHome(t, fp, false), now},
		"no identity yet":      {relayHome(t, "", true), now},
		"an expired consensus": {relayHome(t, fp, true), now.Add(30 * 24 * time.Hour)},
	}
	for name, c := range cases {
		if err := os.WriteFile(filepath.Join(c.home, MonitorFile), []byte("{\"in_consensus\":true}\n"), 0o640); err != nil {
			t.Fatal(err)
		}
		got, err := WriteMonitor(c.home, c.now)
		if err != nil || got != nil {
			t.Errorf("%s: %v, %v", name, got, err)
		}
		if body := readMonitor(t, c.home); body != "{}\n" {
			t.Errorf("%s: monitor.json = %q, want the stale answer replaced by {}", name, body)
		}
	}
}

// A directory authority keeps the full consensus (cached-consensus), not the
// microdescriptor one, and is in it as a relay: the same writer answers for it.
func TestWriteMonitor_directoryAuthorityHome(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "consensus-ns.txt"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := ParseConsensus(bytes.NewReader(raw))
	if err != nil || len(c.Relays) == 0 {
		t.Fatalf("fixture: %v", err)
	}
	now := c.ValidAfter.Add(time.Minute)
	for fingerprint, want := range map[string]bool{c.Relays[0].Fingerprint: true, "00000000000000000000000000000000000000EE": false} {
		home := relayHome(t, fingerprint, false)
		if err := os.WriteFile(filepath.Join(home, DataDirConsensus), raw, 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := WriteMonitor(home, now)
		if err != nil || got == nil || *got != want {
			t.Fatalf("authority %s: %v, %v, want in_consensus %t", fingerprint, got, err, want)
		}
	}
}

func TestWriteMonitor_refusals(t *testing.T) {
	if _, err := WriteMonitor(filepath.Join(t.TempDir(), "absent"), time.Now()); err == nil {
		t.Error("a missing DataDirectory was written to")
	}
	home := relayHome(t, "not a fingerprint", false)
	if _, err := WriteMonitor(home, time.Now()); err == nil {
		t.Error("a malformed fingerprint file was accepted")
	}
	if _, err := os.Stat(filepath.Join(home, MonitorFile)); err == nil {
		t.Error("monitor.json was written for a relay whose state could not be read")
	}
}
