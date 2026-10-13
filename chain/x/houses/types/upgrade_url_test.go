package types

import (
	"encoding/json"
	"net/url"
	"os"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

// upgradeURLVector is one row of testdata/upgrade_url_vectors.json, which core/pkg/planinfo's tests
// read too: both parsers must give the same checksum for every URL in it.
type upgradeURLVector struct {
	Name         string `json:"name"`
	URL          string `json:"url"`
	Checksum     string `json:"checksum"`
	ChainAccepts bool   `json:"chain_accepts"`
}

func readUpgradeURLVectors(t testing.TB) []upgradeURLVector {
	t.Helper()
	raw, err := os.ReadFile("testdata/upgrade_url_vectors.json")
	require.NoError(t, err)
	var vectors []upgradeURLVector
	require.NoError(t, json.Unmarshal(raw, &vectors))
	require.NotEmpty(t, vectors)
	return vectors
}

func TestUpgradeURLChecksum_sharedVectors(t *testing.T) {
	for _, v := range readUpgradeURLVectors(t) {
		t.Run(v.Name, func(t *testing.T) {
			require.Equal(t, v.Checksum, upgradeURLChecksum(v.URL))
			require.Equal(t, v.ChainAccepts, checkUpgradeURL(v.URL) == nil, "checkUpgradeURL(%q)", v.URL)
		})
	}
}

func TestCheckUpgradeURL_refusesChecksumOutsideTheQuery(t *testing.T) {
	b := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	for _, raw := range []string{
		"https://h/p/checksum=sha256:" + b + "&x/oramad?checksum=sha256:" + b,
		"https://h/CHECKSUM=x/oramad?checksum=sha256:" + b,
	} {
		err := checkUpgradeURL(raw)
		require.ErrorContains(t, err, "outside the query", raw)
	}
	require.NoError(t, checkUpgradeURL("https://h/o?x=checksum=1&checksum=sha256:"+b), "the same text inside the query is the query's business")
}

// FuzzUpgradeURLChecksum: whatever the URL, the checksum is "" or exactly the one lowercase
// checksum query value url.ParseQuery finds, and a URL the chain accepts always has one.
func FuzzUpgradeURLChecksum(f *testing.F) {
	for _, v := range readUpgradeURLVectors(f) {
		f.Add(v.URL)
	}
	hex := regexp.MustCompile(`^[0-9a-f]{64}$`)
	f.Fuzz(func(t *testing.T, raw string) {
		sum := upgradeURLChecksum(raw)
		if sum != "" {
			require.Regexp(t, hex, sum)
			u, err := url.Parse(raw)
			require.NoError(t, err)
			query, err := url.ParseQuery(u.RawQuery)
			require.NoError(t, err)
			require.Equal(t, []string{UpgradeChecksumPrefix + sum}, query["checksum"])
		}
		if checkUpgradeURL(raw) == nil {
			require.NotEmpty(t, sum, "an accepted URL names its checksum: %q", raw)
		}
	})
}
