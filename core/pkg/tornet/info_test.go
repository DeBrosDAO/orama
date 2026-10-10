package tornet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadNodeInfo_relayListedInTheConsensus(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, dataDirFingerprint), []byte("OramaAuth1 0000 0000 0000 0000 0000 0000 0000 0000 0000 00B0\n"))
	write(t, filepath.Join(home, dataDirFingerprintEd), []byte("OramaAuth1 AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAE\n"))
	write(t, filepath.Join(home, DataDirConsensus), readFixture(t, "consensus-microdesc.txt"))
	now := time.Date(2026, 10, 8, 12, 10, 0, 0, time.UTC)
	info, err := ReadNodeInfo(home, now)
	if err != nil {
		t.Fatal(err)
	}
	if info.Nickname != "OramaAuth1" || info.Fingerprint != "00000000000000000000000000000000000000B0" || info.Onion != "" {
		t.Fatalf("info = %+v", info)
	}
	c := info.Consensus
	if c == nil || !c.Fresh || !c.Valid || !c.Listed || c.Relays != 3 || c.Exits != 1 || c.Guards != 2 || len(c.ListedFlags) == 0 {
		t.Fatalf("consensus = %+v", c)
	}
	late, err := ReadNodeInfo(home, now.Add(3*time.Hour))
	if err != nil || late.Consensus.Fresh || late.Consensus.Valid {
		t.Fatalf("a stale consensus is reported fresh: %+v %v", late.Consensus, err)
	}
}

func TestReadNodeInfo_aRelayTheNetworkDoesNotListIsNotListed(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, dataDirFingerprint), []byte("Newcomer 1111 1111 1111 1111 1111 1111 1111 1111 1111 1111\n"))
	write(t, filepath.Join(home, dataDirMicrodescConsens), readFixture(t, "consensus-microdesc.txt"))
	info, err := ReadNodeInfo(home, time.Date(2026, 10, 8, 12, 10, 0, 0, time.UTC))
	if err != nil || info.Consensus == nil || info.Consensus.Listed {
		t.Fatalf("info = %+v %v", info, err)
	}
}

func TestReadNodeInfo_onionAndNothingYet(t *testing.T) {
	home := t.TempDir()
	if info, err := ReadNodeInfo(home, time.Now()); err != nil || info.Nickname != "" || info.Consensus != nil {
		t.Fatalf("a DataDirectory tor has not written to = %+v %v", info, err)
	}
	if err := os.MkdirAll(filepath.Join(home, "onion"), 0o700); err != nil {
		t.Fatal(err)
	}
	onion := strings.Repeat("a", 56) + ".onion"
	write(t, filepath.Join(home, "onion", "hostname"), []byte(onion+"\n"))
	info, err := ReadNodeInfo(home, time.Now())
	if err != nil || info.Onion != onion {
		t.Fatalf("info = %+v %v", info, err)
	}
}

func TestReadNodeInfo_aBrokenFileIsAnErrorNotAnEmptyField(t *testing.T) {
	for name, file := range map[string]string{dataDirFingerprint: "garbage", dataDirFingerprintEd: "nick short", DataDirConsensus: "garbage\n"} {
		home := t.TempDir()
		write(t, filepath.Join(home, name), []byte(file))
		if _, err := ReadNodeInfo(home, time.Now()); err == nil {
			t.Errorf("a broken %s was read", name)
		}
	}
}

func TestReadNodeInfo_countsExitsThatAcceptNoPort(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, DataDirConsensus), readFixture(t, "consensus-ns.txt"))
	info, err := ReadNodeInfo(home, time.Date(2026, 10, 8, 12, 10, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	c := info.Consensus
	if c == nil || c.Flavor != "ns" || c.ExitsWithoutPorts != 0 {
		t.Fatalf("consensus = %+v: the fixture's only exit accepts ports", c)
	}
	exitless := strings.Replace(string(readFixture(t, "consensus-ns.txt")), "p accept 1-65535", "p reject 1-65535", 1)
	write(t, filepath.Join(home, DataDirConsensus), []byte(exitless))
	info, err = ReadNodeInfo(home, time.Date(2026, 10, 8, 12, 10, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if c := info.Consensus; c.Exits != 1 || c.ExitsWithoutPorts != 1 {
		t.Fatalf("exits %d without ports %d, want 1 and 1", c.Exits, c.ExitsWithoutPorts)
	}
}

func TestReadNodeInfo_reportsTheOnionTimePeriodTheAuthoritiesVoted(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 10, 0, 0, time.UTC)
	for name, tc := range map[string]struct {
		params string
		want   int
	}{"voted": {"params hsdir_interval=720\n", 720}, "not voted": {"", 0}} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			write(t, filepath.Join(home, dataDirMicrodescConsens), []byte(withParams(t, tc.params)))
			info, err := ReadNodeInfo(home, now)
			if err != nil || info.Consensus == nil {
				t.Fatalf("info = %+v %v", info, err)
			}
			if info.Consensus.HSDirIntervalMinutes != tc.want {
				t.Errorf("hsdir interval = %d, want %d", info.Consensus.HSDirIntervalMinutes, tc.want)
			}
		})
	}
}

func TestEffectiveHSDirInterval_isWhatTorUses(t *testing.T) {
	for name, tc := range map[string]struct {
		params      string
		want        int
		clampedFrom int64
		clamped     bool
	}{
		"not voted":         {"", 0, 0, false},
		"in range":          {"params hsdir_interval=720\n", 720, 0, false},
		"the floor":         {"params hsdir_interval=30\n", 30, 0, false},
		"the ceiling":       {"params hsdir_interval=14400\n", 14400, 0, false},
		"below the floor":   {"params hsdir_interval=29\n", 30, 29, true},
		"zero":              {"params hsdir_interval=0\n", 30, 0, true},
		"above the ceiling": {"params hsdir_interval=14401\n", 14400, 14401, true},
		"huge":              {"params hsdir_interval=9000000000\n", 14400, 9000000000, true},
	} {
		t.Run(name, func(t *testing.T) {
			c, err := ParseConsensus(strings.NewReader(withParams(t, tc.params)))
			if err != nil {
				t.Fatal(err)
			}
			got, from := effectiveHSDirInterval(c)
			if got != tc.want || (from != nil) != tc.clamped || (from != nil && *from != tc.clampedFrom) {
				t.Errorf("effective = %d, clamped from %v; want %d, clamped %t from %d", got, from, tc.want, tc.clamped, tc.clampedFrom)
			}
			info := summarise(c, "", time.Now())
			if info.HSDirIntervalMinutes != tc.want || (info.HSDirIntervalVotedMinutes != nil) != tc.clamped {
				t.Errorf("summary = %d %v, want %d clamped %t", info.HSDirIntervalMinutes, info.HSDirIntervalVotedMinutes, tc.want, tc.clamped)
			}
		})
	}
}
