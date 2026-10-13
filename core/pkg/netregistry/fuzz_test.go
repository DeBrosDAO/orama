package netregistry

import (
	"encoding/json"
	"net"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

// FuzzNetregistryManifest: a network manifest comes from a registry a joining node fetches. For any
// bytes, ParseManifest never panics; an accepted manifest passes Validate, names its seeds as
// distinct lowercase DNS names (never an IP address), carries an https release repository with no
// credentials, query or fragment, and survives being written and read again unchanged. The digest
// of the bytes is stable.
func FuzzNetregistryManifest(f *testing.F) {
	// The stagenet manifest is in the committed corpus (testdata/fuzz, made from networks/stagenet by
	// make -C chain fuzz-corpus), so this target needs nothing outside the core module.
	valid := []byte(`{"name":"stagenet","chain_id":"orama-stagenet-10","genesis_sha256":"` + strings.Repeat("ab", 32) +
		`","seeds":["ns1.example.org","ns2.example.org"],"channel":"nightly","min_version":"0.3.0",` +
		`"release_repo":"https://releases.example.org","release_root_sha256":"` + strings.Repeat("cd", 32) + `","faucet":true}`)
	f.Add(valid)
	f.Add([]byte(`{"name":"x"}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`null`))
	f.Add(append(append([]byte{}, valid...), []byte(`{}`)...))
	f.Fuzz(func(t *testing.T, data []byte) {
		m, err := ParseManifest(data)
		if Digest(data) != Digest(append([]byte(nil), data...)) {
			t.Fatal("Digest is not stable")
		}
		if err != nil {
			return
		}
		if err := m.Validate(); err != nil {
			t.Fatalf("an accepted manifest fails Validate: %v", err)
		}
		seen := map[string]bool{}
		for _, seed := range m.Seeds {
			if net.ParseIP(seed) != nil || seen[seed] || seed != strings.ToLower(seed) || !strings.Contains(seed, ".") || strings.ContainsAny(seed, "/:@ ") {
				t.Fatalf("seed %q is an address, a duplicate or not a lowercase DNS name", seed)
			}
			seen[seed] = true
		}
		u, err := url.Parse(m.ReleaseRepo)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			t.Fatalf("release repository %q is not a plain https URL", m.ReleaseRepo)
		}
		out, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		again, err := ParseManifest(out)
		if err != nil || !reflect.DeepEqual(again, m) {
			t.Fatalf("a manifest does not survive a round trip (%v)", err)
		}
		if m.Announced() != (m.GenesisSHA256 == "") || (m.CheckCreated() == nil) == m.Announced() {
			t.Fatal("Announced and CheckCreated disagree")
		}
	})
}
