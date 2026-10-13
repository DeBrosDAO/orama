package reporter

import (
	"bytes"
	"crypto/sha1" //nolint:gosec // Tor's relay digest is SHA-1; the test builds votes the way tor writes them.
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const testAuthorityHex = "abababababababababababababababababababab"

func testAuthority() [fingerprintLen]byte {
	var a [fingerprintLen]byte
	b, _ := hex.DecodeString(testAuthorityHex)
	copy(a[:], b)
	return a
}

// relaySpec is one relay as testVote renders it.
type relaySpec struct {
	n        byte
	flags    string
	measured string // the w line; empty for none
	noEd     bool
}

func fp(n byte) [fingerprintLen]byte {
	var f [fingerprintLen]byte
	for i := range f {
		f[i] = n
	}
	return f
}

func ed(n byte) []byte { return bytes.Repeat([]byte{0x40 + n}, ed25519Len) }

func b64(b []byte) string { return base64.RawStdEncoding.EncodeToString(b) }

// testVote renders a vote in the dir-spec text form tor writes.
func testVote(authority string, validAfter time.Time, relays ...relaySpec) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "network-status-version 3\nvote-status vote\nvalid-after %s\n", validAfter.Format(voteTimeLayout))
	fmt.Fprintf(&sb, "dir-source dirauth1 %s 192.0.2.10 192.0.2.10 31021 31020\n", authority)
	for _, r := range relays {
		f := fp(r.n)
		digest := sha1.Sum(f[:]) //nolint:gosec
		fmt.Fprintf(&sb, "r relay%d %s %s 2026-10-08 09:40:11 198.51.100.%d 31020 0\n", r.n, b64(f[:]), b64(digest[:]), r.n)
		fmt.Fprintf(&sb, "s %s\nv Tor 0.4.8.12\n", r.flags)
		if r.measured != "" {
			fmt.Fprintf(&sb, "w %s\n", r.measured)
		}
		if r.noEd {
			sb.WriteString("id ed25519 none\n")
		} else {
			fmt.Fprintf(&sb, "id ed25519 %s\n", b64(ed(r.n)))
		}
	}
	sb.WriteString("directory-footer\n")
	return sb.String()
}

func mustParse(t *testing.T, doc string) Vote {
	t.Helper()
	v, err := ParseVote(strings.NewReader(doc))
	require.NoError(t, err)
	return v
}

func TestParseVote_fixture(t *testing.T) {
	f, err := os.Open(filepath.Join("testdata", "vote-sample.vote"))
	require.NoError(t, err)
	defer f.Close()
	v, err := ParseVote(f)
	require.NoError(t, err)

	require.Equal(t, testAuthority(), v.Authority)
	require.Equal(t, time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC), v.ValidAfter)
	require.Len(t, v.Routers, 3)

	guard := v.Routers[0]
	require.Equal(t, fp(1), guard.Fingerprint)
	require.Equal(t, ed(1), guard.Ed25519)
	require.True(t, guard.Flags["Guard"] && guard.Flags["Running"])
	require.True(t, guard.HasMeasured)
	require.EqualValues(t, 5000, guard.Measured, "the measured figure, not the advertised Bandwidth=5200")

	selfReport := v.Routers[2]
	require.False(t, selfReport.HasMeasured, "a relay the authority did not measure has no weight, whatever it advertises")
}

func TestParseVote_noEd25519Identity(t *testing.T) {
	v := mustParse(t, testVote(testAuthorityHex, time.Now().UTC().Truncate(time.Second),
		relaySpec{n: 1, flags: "Running", measured: "Measured=10", noEd: true}))
	require.Empty(t, v.Routers[0].Ed25519)
}

func TestParseVote_refusals(t *testing.T) {
	good := testVote(testAuthorityHex, time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC), relaySpec{n: 1, flags: "Running", measured: "Measured=10"})
	cases := map[string]string{
		"a consensus is not a vote":     strings.Replace(good, "vote-status vote", "vote-status consensus", 1),
		"no valid-after":                strings.Replace(good, "valid-after 2026-10-08 10:00:00\n", "", 1),
		"no dir-source":                 strings.Replace(good, "dir-source", "dir-sourcX", 1),
		"short identity":                strings.Replace(good, "dir-source dirauth1 "+testAuthorityHex, "dir-source dirauth1 abab", 1),
		"r line too short":              strings.Replace(good, " 198.51.100.1 31020 0", "", 1),
		"identity not base64":           strings.Replace(good, "r relay1 "+b64(bytes.Repeat([]byte{1}, 20)), "r relay1 !!!", 1),
		"Measured is not a number":      strings.Replace(good, "Measured=10", "Measured=ten", 1),
		"ed25519 key of the wrong size": strings.Replace(good, b64(ed(1)), b64(ed(1)[:16]), 1),
		"the same relay twice":          good[:strings.Index(good, "directory-footer")] + good[strings.Index(good, "r relay1"):],
	}
	for name, doc := range cases {
		_, err := ParseVote(strings.NewReader(doc))
		require.Error(t, err, name)
	}
}

func TestParseVote_emptyInput(t *testing.T) {
	_, err := ParseVote(strings.NewReader(""))
	require.ErrorIs(t, err, ErrNotAVote)
}

func TestParseVoteHeader_stopsBeforeTheRelays(t *testing.T) {
	doc := testVote(testAuthorityHex, time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC), relaySpec{n: 1, flags: "Running"})
	v, err := ParseVoteHeader(strings.NewReader(doc))
	require.NoError(t, err)
	require.Empty(t, v.Routers)
	require.Equal(t, testAuthority(), v.Authority)
}

func TestParseVote_aVoteStillBeingWrittenIsRefused(t *testing.T) {
	doc := testVote(testAuthorityHex, time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC), relaySpec{n: 1, flags: "Running"})
	cut := strings.TrimSuffix(doc, "directory-footer\n")
	_, err := ParseVote(strings.NewReader(cut))
	require.ErrorIs(t, err, ErrNotAVote)
	require.ErrorContains(t, err, "still being written")

	header, err := ParseVoteHeader(strings.NewReader(cut))
	require.NoError(t, err, "the header of a vote being written is enough to know it is outside the window")
	require.Equal(t, testAuthority(), header.Authority)
}
