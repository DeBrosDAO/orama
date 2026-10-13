package reporter

import (
	"bytes"
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
)

// FuzzReporterParseVote: the Tor network-status vote the reporter reads is a file written by
// another process. ParseVote never panics, answers the same twice, and an accepted vote has the
// digest of its bytes, relays with distinct fingerprints and flags, a header that ParseVoteHeader
// reads the same way, and no more relays than the cap. ParseVoteHeader never panics either.
func FuzzReporterParseVote(f *testing.F) {
	sample, err := os.ReadFile(filepath.Join("testdata", "vote-sample.vote"))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(sample)
	f.Add(sample[:len(sample)/2])
	f.Add([]byte("network-status-version 3\nvote-status vote\nvalid-after 2026-01-01 00:00:00\ndirectory-footer\n"))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, raw []byte) {
		vote, err := ParseVote(bytes.NewReader(raw))
		header, headerErr := ParseVoteHeader(bytes.NewReader(raw))
		if again, err2 := ParseVote(bytes.NewReader(raw)); (err == nil) != (err2 == nil) || (err == nil && again.Digest != vote.Digest) {
			t.Fatal("ParseVote is not deterministic")
		}
		if err != nil {
			return
		}
		if vote.Digest != sha256.Sum256(raw) {
			t.Fatal("the vote digest is not the SHA-256 of its bytes")
		}
		if len(vote.Routers) > maxVoteRouters {
			t.Fatalf("%d relays, cap %d", len(vote.Routers), maxVoteRouters)
		}
		seen := map[[fingerprintLen]byte]bool{}
		for _, r := range vote.Routers {
			if seen[r.Fingerprint] {
				t.Fatalf("relay %x is listed twice", r.Fingerprint)
			}
			seen[r.Fingerprint] = true
			if len(r.Ed25519) != 0 && len(r.Ed25519) != ed25519Len {
				t.Fatalf("relay %s has a %d byte ed25519 identity", r.Nickname, len(r.Ed25519))
			}
			if !r.HasMeasured && r.Measured != 0 {
				t.Fatalf("relay %s has a measured bandwidth the authority did not state", r.Nickname)
			}
		}
		if headerErr != nil {
			t.Fatalf("ParseVote accepts what ParseVoteHeader refuses: %v", headerErr)
		}
		if header.Authority != vote.Authority || !header.ValidAfter.Equal(vote.ValidAfter) {
			t.Fatal("the header of a vote reads differently on its own")
		}
	})
}
