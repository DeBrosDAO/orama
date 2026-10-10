package tornet

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
)

// testNetwork is a valid three-authority network on public addresses.
func testNetwork() Network {
	n := Network{
		Name:                  "orama-teststage",
		Private:               true,
		VotingIntervalMinutes: 30,
		VoteDelaySeconds:      300,
		DistDelaySeconds:      300,
	}
	for i, addr := range []string{"57.129.166.16", "57.129.166.17", "161.97.184.199"} {
		n.Authorities = append(n.Authorities, Authority{
			Nickname:    fmt.Sprintf("OramaAuth%d", i+1),
			Address:     addr,
			ORPort:      31020,
			DirPort:     31021,
			V3Ident:     fmt.Sprintf("%040X", 0xA0+i),
			Fingerprint: fmt.Sprintf("%040X", 0xB0+i),
			Ed25519ID:   base64.RawStdEncoding.EncodeToString(append(make([]byte, 31), byte(i+1))),
		})
	}
	return n
}

func TestNetwork_validAcceptedAndRoundTrips(t *testing.T) {
	body, err := testNetwork().Marshal()
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseNetwork(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Authorities) != 3 || got.Name != "orama-teststage" {
		t.Fatalf("round trip lost data: %+v", got)
	}
}

func TestNetwork_refusals(t *testing.T) {
	cases := map[string]func(*Network){
		"two authorities":                     func(n *Network) { n.Authorities = n.Authorities[:2] },
		"no authorities":                      func(n *Network) { n.Authorities = nil },
		"empty name":                          func(n *Network) { n.Name = "" },
		"uppercase name":                      func(n *Network) { n.Name = "Orama" },
		"interval not a divisor":              func(n *Network) { n.VotingIntervalMinutes = 50 },
		"interval too short":                  func(n *Network) { n.VotingIntervalMinutes = 1 },
		"delays fill the round":               func(n *Network) { n.VoteDelaySeconds, n.DistDelaySeconds = 500, 500 },
		"vote delay too low":                  func(n *Network) { n.VoteDelaySeconds = 5 },
		"private address":                     func(n *Network) { n.Authorities[0].Address = "10.0.0.5" },
		"namespace address":                   func(n *Network) { n.Authorities[0].Address = "198.18.0.2" },
		"ipv6 address":                        func(n *Network) { n.Authorities[0].Address = "2606:4700::1111" },
		"same nickname":                       func(n *Network) { n.Authorities[1].Nickname = n.Authorities[0].Nickname },
		"same address":                        func(n *Network) { n.Authorities[1].Address = n.Authorities[0].Address },
		"same fingerprint":                    func(n *Network) { n.Authorities[1].Fingerprint = n.Authorities[0].Fingerprint },
		"short fingerprint":                   func(n *Network) { n.Authorities[0].Fingerprint = "ABCD" },
		"lowercase v3 ident":                  func(n *Network) { n.Authorities[0].V3Ident = strings.ToLower(n.Authorities[0].V3Ident) },
		"same ports":                          func(n *Network) { n.Authorities[0].DirPort = n.Authorities[0].ORPort },
		"port zero":                           func(n *Network) { n.Authorities[0].ORPort = 0 },
		"bad nickname":                        func(n *Network) { n.Authorities[0].Nickname = "has space" },
		"bad ed25519 id":                      func(n *Network) { n.Authorities[0].Ed25519ID = "short" },
		"negative hsdir uptime":               func(n *Network) { n.HSDirMinUptimeHours = -1 },
		"hsdir uptime past 96h":               func(n *Network) { n.HSDirMinUptimeHours = 97 },
		"run past Tor's longest hsdir period": func(n *Network) { n.VotingIntervalMinutes = 720 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			n := testNetwork()
			mutate(&n)
			if err := n.Validate(); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestParseNetwork_rejectsUnknownFieldsAndTrailingData(t *testing.T) {
	good, err := testNetwork().Marshal()
	if err != nil {
		t.Fatal(err)
	}
	withExtra := strings.Replace(string(good), `"name"`, `"allow_exits": true, "name"`, 1)
	if _, err := ParseNetwork([]byte(withExtra)); err == nil {
		t.Fatal("a misspelt key was accepted")
	}
	if _, err := ParseNetwork(append(good, []byte(`{}`)...)); err == nil {
		t.Fatal("trailing data was accepted")
	}
	if _, err := ParseNetwork(nil); err == nil {
		t.Fatal("an empty file was accepted")
	}
}

func TestParseNetwork_uppercasesFingerprints(t *testing.T) {
	n := testNetwork()
	body, _ := n.Marshal()
	lowered := strings.ReplaceAll(string(body), n.Authorities[0].V3Ident, strings.ToLower(n.Authorities[0].V3Ident))
	got, err := ParseNetwork([]byte(lowered))
	if err != nil {
		t.Fatal(err)
	}
	if got.Authorities[0].V3Ident != n.Authorities[0].V3Ident {
		t.Fatalf("v3 ident = %s", got.Authorities[0].V3Ident)
	}
}

func TestDirAuthorityLines_format(t *testing.T) {
	lines := testNetwork().DirAuthorityLines()
	if len(lines) != 3 {
		t.Fatalf("%d lines", len(lines))
	}
	want := "DirAuthority OramaAuth1 orport=31020 v3ident=" + fmt.Sprintf("%040X", 0xA0) + " 57.129.166.16:31021 " + fmt.Sprintf("%040X", 0xB0)
	if lines[0] != want {
		t.Fatalf("line = %q, want %q", lines[0], want)
	}
}

func TestAuthorityAt(t *testing.T) {
	n := testNetwork()
	if a, ok := n.AuthorityAt("57.129.166.17"); !ok || a.Nickname != "OramaAuth2" {
		t.Fatalf("AuthorityAt = %+v %v", a, ok)
	}
	if _, ok := n.AuthorityAt("1.2.3.4"); ok {
		t.Fatal("found an authority that is not there")
	}
}

func TestNetwork_HSDirIntervalMinutes_isOneSharedRandomRun(t *testing.T) {
	for interval, want := range map[int]int{5: 120, 30: 720, 60: 1440, 480: 11520} {
		n := testNetwork()
		n.VotingIntervalMinutes, n.VoteDelaySeconds, n.DistDelaySeconds = interval, minDelaySeconds, minDelaySeconds
		if err := n.Validate(); err != nil {
			t.Fatalf("interval %d: %v", interval, err)
		}
		if got := n.HSDirIntervalMinutes(); got != want {
			t.Errorf("voting interval %d min: onion service time period = %d min, want %d (24 intervals)", interval, got, want)
		}
	}
}

func TestNetwork_votingIntervalBounds(t *testing.T) {
	for _, tc := range []struct {
		interval int
		want     string // empty: valid
	}{
		{5, ""},
		{480, ""},
		{4, "at least 5"},
		{7, "divide 24 hours"},
		{481, "divide 24 hours"},
		{601, "use at most 480"},
		{720, "use at most 480"},
		{1440, "use at most 480"},
	} {
		n := testNetwork()
		n.VotingIntervalMinutes, n.VoteDelaySeconds, n.DistDelaySeconds = tc.interval, minDelaySeconds, minDelaySeconds
		err := n.Validate()
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("interval %d refused: %v", tc.interval, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("interval %d: error %v, want one containing %q", tc.interval, err, tc.want)
		}
	}
}

func TestNetwork_maxVotingIntervalIsTheLargestValidOne(t *testing.T) {
	largest := 0
	for interval := 1; interval <= 2*minutesPerDay; interval++ {
		n := testNetwork()
		n.VotingIntervalMinutes, n.VoteDelaySeconds, n.DistDelaySeconds = interval, minDelaySeconds, minDelaySeconds
		if n.validateSchedule() == nil {
			largest = interval
		}
	}
	if largest != maxVotingIntervalMinutes {
		t.Errorf("the largest voting interval validateSchedule accepts is %d, maxVotingIntervalMinutes is %d", largest, maxVotingIntervalMinutes)
	}
}

// Tor derives the onion service time period from the voting interval i: the
// shared-random protocol (SRV) runs 24 x i minutes, starting at multiples of the
// run since the epoch, and period k starts at k x L + 12 x i minutes, with L the
// hsdir_interval. With L one run, a period starts half a run after an SRV run
// does, so a service's rotation (at each run start) never falls on a period
// boundary but half a period after a period start.
func TestNetwork_periodStartsHalfARunAfterAnSRVStart(t *testing.T) {
	const roundsPerPhase = 12
	valid := 0
	for interval := 1; interval <= minutesPerDay; interval++ {
		n := testNetwork()
		n.VotingIntervalMinutes, n.VoteDelaySeconds, n.DistDelaySeconds = interval, minDelaySeconds, minDelaySeconds
		if n.validateSchedule() != nil {
			continue
		}
		valid++
		run, period := sharedRandomRounds*interval, n.HSDirIntervalMinutes()
		for k := 0; k < 4; k++ {
			start := k*period + roundsPerPhase*interval
			srvStart := start / run * run
			if start-srvStart != run/2 {
				t.Errorf("interval %d, period %d: starts %d min after an SRV start, want %d (half a run)", interval, k, start-srvStart, run/2)
			}
			if nextRotation := srvStart + run; nextRotation-start != period/2 {
				t.Errorf("interval %d, period %d: next rotation is %d min after the period start, want %d (half a period)", interval, k, nextRotation-start, period/2)
			}
		}
	}
	if valid == 0 {
		t.Fatal("no voting interval is valid")
	}
}
