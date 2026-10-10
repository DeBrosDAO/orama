package netregistry

import (
	"errors"
	"strings"
	"testing"
)

func TestParseManifest_valid(t *testing.T) {
	want := validManifest()
	got, err := ParseManifest(marshalManifest(t, want))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if got.ChainID != want.ChainID || got.Seeds[1] != want.Seeds[1] || !got.Faucet {
		t.Errorf("parsed %+v, want %+v", got, want)
	}
}

func TestParseManifest_refusesWhatItDoesNotUnderstand(t *testing.T) {
	valid := string(marshalManifest(t, validManifest()))
	for name, data := range map[string]string{
		"unknown field":   strings.Replace(valid, `"faucet"`, `"treasury":"x","faucet"`, 1),
		"trailing object": valid + valid,
		"trailing text":   valid + " garbage",
		"not an object":   `[]`,
		"empty":           ``,
		"oversized":       valid + strings.Repeat(" ", maxManifestBytes),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseManifest([]byte(data)); err == nil {
				t.Fatal("ParseManifest accepted it")
			}
		})
	}
}

func TestManifestValidate_fields(t *testing.T) {
	for _, tc := range []struct {
		name   string
		edit   func(*Manifest)
		wantIn string
	}{
		{"empty name", func(m *Manifest) { m.Name = "" }, "name"},
		{"name with a slash", func(m *Manifest) { m.Name = "a/b" }, "name"},
		{"uppercase name", func(m *Manifest) { m.Name = "Stagenet" }, "name"},
		{"chain id with a space", func(m *Manifest) { m.ChainID = "orama stagenet" }, "chain_id"},
		{"chain id ending in a dash", func(m *Manifest) { m.ChainID = "orama-" }, "chain_id"},
		{"chain id too long", func(m *Manifest) { m.ChainID = strings.Repeat("a", 49) }, "chain_id"},
		{"genesis digest uppercase", func(m *Manifest) { m.GenesisSHA256 = strings.ToUpper(m.GenesisSHA256) }, "genesis_sha256"},
		{"genesis digest short", func(m *Manifest) { m.GenesisSHA256 = "abcd" }, "genesis_sha256"},
		{"no seeds", func(m *Manifest) { m.Seeds = nil }, "seeds"},
		{"seed is an IPv4 address", func(m *Manifest) { m.Seeds = []string{"203.0.113.9"} }, "DNS name"},
		{"seed is an IPv6 address", func(m *Manifest) { m.Seeds = []string{"2001:db8::1"} }, "IP"},
		{"seed with a port", func(m *Manifest) { m.Seeds = []string{"seed1.example.org:31000"} }, "seed"},
		{"seed with one label", func(m *Manifest) { m.Seeds = []string{"localhost"} }, "two labels"},
		{"seed listed twice", func(m *Manifest) { m.Seeds = []string{"a.example.org", "a.example.org"} }, "twice"},
		{"unknown channel", func(m *Manifest) { m.Channel = "beta" }, "channel"},
		{"dev channel without a branch", func(m *Manifest) { m.Channel = "dev/" }, "channel"},
		{"min version with a prefix", func(m *Manifest) { m.MinVersion = "v0.3.0" }, "min_version"},
		{"min version with two parts", func(m *Manifest) { m.MinVersion = "0.3" }, "min_version"},
		{"http release repo", func(m *Manifest) { m.ReleaseRepo = "http://releases.example.org/" }, "https"},
		{"release repo without a host", func(m *Manifest) { m.ReleaseRepo = "https:///x" }, "host"},
		{"release repo with a password", func(m *Manifest) { m.ReleaseRepo = "https://u:p@releases.example.org/" }, "user"},
		{"release repo with a query", func(m *Manifest) { m.ReleaseRepo = "https://releases.example.org/?x=1" }, "query"},
		{"root digest missing", func(m *Manifest) { m.ReleaseRootSHA256 = "" }, "release_root_sha256"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := validManifest()
			tc.edit(&m)
			err := m.Validate()
			if err == nil {
				t.Fatal("Validate accepted it")
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("error %q does not mention %q", err, tc.wantIn)
			}
		})
	}
}

func TestManifestValidate_acceptsEveryChannelForm(t *testing.T) {
	for _, channel := range []string{"nightly", "main", "dev/e3306-c", "dev/feature.x_1"} {
		m := validManifest()
		m.Channel = channel
		if err := m.Validate(); err != nil {
			t.Errorf("channel %q: %v", channel, err)
		}
	}
}

func TestVerifyGenesis_matchAndMismatch(t *testing.T) {
	m := validManifest()
	if err := m.VerifyGenesis(testGenesis); err != nil {
		t.Fatalf("the pinned genesis was refused: %v", err)
	}
	err := m.VerifyGenesis(append([]byte(nil), append(testGenesis, ' ')...))
	if !errors.Is(err, ErrGenesisMismatch) {
		t.Fatalf("a changed genesis gave %v, want ErrGenesisMismatch", err)
	}
	if err := m.VerifyGenesis(nil); !errors.Is(err, ErrGenesisMismatch) {
		t.Fatalf("an empty genesis gave %v, want ErrGenesisMismatch", err)
	}
}

func TestVerifyRoot_matchAndMismatch(t *testing.T) {
	m := validManifest()
	if err := m.VerifyRoot(testRoot); err != nil {
		t.Fatalf("the pinned root was refused: %v", err)
	}
	if err := m.VerifyRoot([]byte(`{"signed":{"_type":"other"}}`)); !errors.Is(err, ErrRootMismatch) {
		t.Fatalf("another root gave %v, want ErrRootMismatch", err)
	}
}

func TestManifestMarshal_roundTripsAndRefusesInvalid(t *testing.T) {
	m := validManifest()
	data, err := m.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(data), "}\n") {
		t.Errorf("a manifest ends with a newline, got %q", data[len(data)-3:])
	}
	back, err := ParseManifest(data)
	if err != nil || back.ChainID != m.ChainID {
		t.Fatalf("round trip: %v %+v", err, back)
	}
	m.Seeds = nil
	if _, err := m.Marshal(); err == nil {
		t.Error("Marshal wrote an invalid manifest")
	}
}
