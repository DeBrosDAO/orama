package tornet

import (
	"os"
	"strings"
	"testing"
	"time"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestParseConsensus_fixture(t *testing.T) {
	c, err := ParseConsensus(strings.NewReader(string(readFixture(t, "consensus-microdesc.txt"))))
	if err != nil {
		t.Fatal(err)
	}
	if c.Flavor != "microdesc" {
		t.Errorf("flavor = %q", c.Flavor)
	}
	if want := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC); !c.ValidAfter.Equal(want) {
		t.Errorf("valid-after = %v", c.ValidAfter)
	}
	if len(c.Relays) != 3 || c.Signatures != 2 {
		t.Fatalf("relays %d signatures %d", len(c.Relays), c.Signatures)
	}
	if c.Running() != 3 || c.Exits() != 1 || c.Guards() != 2 {
		t.Errorf("running %d exits %d guards %d", c.Running(), c.Exits(), c.Guards())
	}
	r, ok := c.Listed("b0")
	if ok {
		t.Fatalf("a short fingerprint matched: %+v", r)
	}
	r, ok = c.Listed("00000000000000000000000000000000000000B0")
	if !ok || r.Nickname != "OramaAuth1" || r.Address != "57.129.166.16" || r.ORPort != 31020 || r.Bandwidth != 5000 || !r.Measured {
		t.Fatalf("relay = %+v listed %v", r, ok)
	}
	if r2, _ := c.Listed("00000000000000000000000000000000000000c0"); r2.Measured || r2.Bandwidth != 900 {
		t.Errorf("unmeasured relay = %+v", r2)
	}
}

func TestConsensus_freshness(t *testing.T) {
	c, err := ParseConsensus(strings.NewReader(string(readFixture(t, "consensus-microdesc.txt"))))
	if err != nil {
		t.Fatal(err)
	}
	at := func(m int) time.Time { return time.Date(2026, 10, 8, 12, m, 0, 0, time.UTC) }
	if !c.Fresh(at(10)) || c.Fresh(at(30)) || c.Fresh(at(-1)) {
		t.Error("fresh window is [valid-after, fresh-until)")
	}
	if !c.Valid(at(100)) || c.Valid(at(150)) {
		t.Error("valid window is [valid-after, valid-until)")
	}
}

func TestParseConsensus_refusesWhatIsNotAConsensus(t *testing.T) {
	good := string(readFixture(t, "consensus-microdesc.txt"))
	cases := map[string]string{
		"empty":             "",
		"no times":          "network-status-version 3\n",
		"bad time":          strings.Replace(good, "valid-after 2026-10-08 12:00:00", "valid-after yesterday", 1),
		"short router line": strings.Replace(good, "r OramaAuth1", "r", 1),
		"bad identity":      strings.Replace(good, "r OramaAuth1 AAAAAAAAAAAAAAAAAAAAAAAAALA", "r OramaAuth1 AAAA", 1),
		"no version line":   strings.Replace(good, "network-status-version 3 microdesc\n", "", 1),
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseConsensus(strings.NewReader(doc)); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestParseConsensus_fullDocumentHasNoMicrodescFlavor(t *testing.T) {
	doc := strings.Replace(string(readFixture(t, "consensus-microdesc.txt")), "network-status-version 3 microdesc", "network-status-version 3", 1)
	c, err := ParseConsensus(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	if c.Flavor != "ns" {
		t.Errorf("flavor = %q", c.Flavor)
	}
}

func TestConsensus_exitsWithoutPorts(t *testing.T) {
	doc := strings.Join([]string{
		"network-status-version 3", "vote-status consensus", "valid-after 2026-10-09 17:30:00",
		"fresh-until 2026-10-09 18:00:00", "valid-until 2026-10-09 19:00:00", "known-flags Exit Running",
		"r ExitA AAAAAAAAAAAAAAAAAAAAAAAAALA yrThRCjxnDUQ/pst/UHbtFpjl4c 2026-10-09 11:49:00 192.5.5.241 31020 0", "s Exit Running", "p reject 1-65535",
		"r ExitB AAAAAAAAAAAAAAAAAAAAAAAAALE yrThRCjxnDUQ/pst/UHbtFpjl4d 2026-10-09 11:49:00 192.5.5.242 31020 0", "s Exit Running", "p reject 25,119",
		"r Relay AAAAAAAAAAAAAAAAAAAAAAAAAMA yrThRCjxnDUQ/pst/UHbtFpjl4e 2026-10-09 11:49:00 192.5.5.243 31020 0", "s Running", "p reject 1-65535",
		"r NoSummary AAAAAAAAAAAAAAAAAAAAAAAAAME yrThRCjxnDUQ/pst/UHbtFpjl4f 2026-10-09 11:49:00 192.5.5.244 31020 0", "s Exit Running",
		"directory-footer", "",
	}, "\n")
	c, err := ParseConsensus(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	if c.Exits() != 3 || c.ExitsWithoutPorts() != 1 {
		t.Errorf("exits %d, without ports %d, want 3 and 1: a relay that is not an exit and an entry with no summary are not counted", c.Exits(), c.ExitsWithoutPorts())
	}
	if got := c.Relays[0].Policy; got != "reject 1-65535" {
		t.Errorf("policy = %q", got)
	}
}

func withParams(t *testing.T, params string) string {
	t.Helper()
	good := string(readFixture(t, "consensus-microdesc.txt"))
	const anchor = "known-flags Authority Exit Fast Guard HSDir Running Stable V2Dir Valid\n"
	if !strings.Contains(good, anchor) {
		t.Fatal("the fixture has no known-flags line to put the params line after")
	}
	return strings.Replace(good, anchor, anchor+params, 1)
}

func TestParseConsensus_readsTheNetworkParameters(t *testing.T) {
	c, err := ParseConsensus(strings.NewReader(withParams(t, "params AuthDirMaxServersPerAddr=1 hsdir_interval=720\n")))
	if err != nil {
		t.Fatal(err)
	}
	if c.Params["hsdir_interval"] != 720 || c.Params["AuthDirMaxServersPerAddr"] != 1 || len(c.Params) != 2 {
		t.Errorf("params = %v", c.Params)
	}
	bare, err := ParseConsensus(strings.NewReader(string(readFixture(t, "consensus-microdesc.txt"))))
	if err != nil {
		t.Fatal(err)
	}
	if len(bare.Params) != 0 {
		t.Errorf("a consensus with no params line has params %v", bare.Params)
	}
}

func TestParseConsensus_refusesABrokenParameter(t *testing.T) {
	for name, line := range map[string]string{
		"not a number":            "params hsdir_interval=soon\n",
		"no value":                "params hsdir_interval\n",
		"no key":                  "params =720\n",
		"overflowing":             "params hsdir_interval=99999999999999999999\n",
		"duplicate":               "params hsdir_interval=720 hsdir_interval=1440\n",
		"duplicate, not adjacent": "params hsdir_interval=720 AuthDirMaxServersPerAddr=1 hsdir_interval=720\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseConsensus(strings.NewReader(withParams(t, line))); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestParseConsensus_anEmptyParamsLineHasNoParameters(t *testing.T) {
	for _, line := range []string{"params\n", "params \n"} {
		c, err := ParseConsensus(strings.NewReader(withParams(t, line)))
		if err != nil {
			t.Fatalf("%q: %v", line, err)
		}
		if len(c.Params) != 0 {
			t.Errorf("%q: params = %v, want none", line, c.Params)
		}
	}
}
