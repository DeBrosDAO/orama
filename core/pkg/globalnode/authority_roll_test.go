package globalnode

import (
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/tornet"
)

const (
	rollSelf  = "00000000000000000000000000000000000000B0"
	rollPeer  = "00000000000000000000000000000000000000B1"
	rollOther = "00000000000000000000000000000000000000B2"
	longAgo   = "2026-10-09 14:10:51"
)

var rollNow = time.Date(2026, 10, 10, 2, 40, 0, 0, time.UTC)

func rollNetwork() tornet.Network {
	n := tornet.Network{Name: "orama-teststage", Private: true, VotingIntervalMinutes: 30, VoteDelaySeconds: 300, DistDelaySeconds: 300}
	for i, fp := range []string{rollSelf, rollPeer, rollOther} {
		n.Authorities = append(n.Authorities, tornet.Authority{Nickname: "Auth" + string(rune('A'+i)), Fingerprint: fp})
	}
	return n
}

// rollHome is the DataDirectory of the authority rollSelf, whose full consensus lists the other
// two authorities with the given descriptor publication times.
func rollHome(t *testing.T, peerAt, otherAt string, valid bool) string {
	t.Helper()
	home := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(home, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("fingerprint", "AuthA 0000 0000 0000 0000 0000 0000 0000 0000 0000 00B0\n")
	validUntil := "2026-10-10 03:30:00"
	if !valid {
		validUntil = "2026-10-10 02:30:00"
	}
	doc := "network-status-version 3\nvote-status consensus\nvalid-after 2026-10-10 02:00:00\nfresh-until 2026-10-10 02:30:00\nvalid-until " + validUntil + "\n"
	for _, r := range []struct{ nick, fp, at string }{{"AuthA", rollSelf, longAgo}, {"AuthB", rollPeer, peerAt}, {"AuthC", rollOther, otherAt}} {
		id, err := hex.DecodeString(r.fp)
		if err != nil {
			t.Fatal(err)
		}
		doc += "r " + r.nick + " " + base64.RawStdEncoding.EncodeToString(id) + " AAAAAAAAAAAAAAAAAAAAAAAAAAA " + r.at + " 57.129.166.16 31020 31021\ns Running Valid\n"
	}
	write(tornet.DataDirConsensus, doc)
	return home
}

func TestCheckAuthorityRoll_refusesWhileAnotherAuthorityIsLearning(t *testing.T) {
	home := rollHome(t, "2026-10-10 02:26:49", longAgo, true)
	err := checkAuthorityRoll(rollNetwork(), home, rollNow)
	if err == nil || !strings.Contains(err.Error(), "AuthB") || strings.Contains(err.Error(), "AuthC") || !strings.Contains(err.Error(), "30m0s") {
		t.Fatalf("err = %v, want it to name AuthB only and the 30 minute window", err)
	}
}

func TestCheckAuthorityRoll_allowsWhenTheOthersHaveBeenUpAWhile(t *testing.T) {
	home := rollHome(t, "2026-10-10 02:05:00", longAgo, true)
	if err := checkAuthorityRoll(rollNetwork(), home, rollNow); err != nil {
		t.Fatal(err)
	}
}

func TestCheckAuthorityRoll_cannotJudgeWithoutAValidFullConsensus(t *testing.T) {
	expired := rollHome(t, longAgo, longAgo, false)
	empty := t.TempDir()
	if err := os.WriteFile(filepath.Join(empty, "fingerprint"), []byte("AuthA 0000 0000 0000 0000 0000 0000 0000 0000 0000 00B0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	noFingerprint := t.TempDir()
	for name, home := range map[string]string{"expired consensus": expired, "no consensus yet": empty, "no fingerprint yet": noFingerprint} {
		t.Run(name, func(t *testing.T) {
			err := checkAuthorityRoll(rollNetwork(), home, rollNow)
			if err == nil || !strings.Contains(err.Error(), "cannot tell whether the other authorities are voting") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestCheckAuthorityRoll_aMicrodescriptorConsensusHasNoPublicationTimes(t *testing.T) {
	home := rollHome(t, longAgo, longAgo, true)
	path := filepath.Join(home, tornet.DataDirConsensus)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	md := strings.Replace(string(raw), "network-status-version 3\n", "network-status-version 3 microdesc\n", 1)
	if err := os.WriteFile(filepath.Join(home, "cached-microdesc-consensus"), []byte(md), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := checkAuthorityRoll(rollNetwork(), home, rollNow); err == nil || !strings.Contains(err.Error(), "no publication times") {
		t.Fatalf("err = %v", err)
	}
}
