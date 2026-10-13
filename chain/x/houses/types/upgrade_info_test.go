package types

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	infoBinarySum   = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	infoVerifierSum = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func validInfo() string {
	return `{"binaries":{"linux/amd64":"https://releases.example/oramad?checksum=sha256:` + infoBinarySum +
		`"},"verifiers":{"linux/amd64":"https://releases.example/orama-orchard-verifier?checksum=sha256:` + infoVerifierSum + `"}}`
}

func TestParseUpgradeInfo_readsBothChecksums(t *testing.T) {
	info, err := ParseUpgradeInfo(validInfo())
	require.NoError(t, err)
	require.Equal(t, infoBinarySum, info.BinaryChecksum("linux/amd64"))
	require.Equal(t, infoVerifierSum, info.VerifierChecksum("linux/amd64"))
	require.Empty(t, info.BinaryChecksum("darwin/arm64"), "a platform the info does not name has no checksum")
}

func TestParseUpgradeInfo_acceptsAnExtraPlatformBesideTheRequiredOne(t *testing.T) {
	extra := strings.Replace(validInfo(), `"binaries":{`, `"binaries":{"linux/arm64":"https://releases.example/oramad-arm?checksum=sha256:`+infoBinarySum+`",`, 1)
	_, err := ParseUpgradeInfo(extra)
	require.NoError(t, err)
}

func TestParseUpgradeInfo_refusesAnythingElse(t *testing.T) {
	sum := func(n string) string { return "sha256:" + strings.Repeat(n, 64) }
	for name, tc := range map[string]struct{ info, want string }{
		"empty":                   {"", "1 to"},
		"not json":                {"binaries", "not the upgrade-info JSON"},
		"a bare URL":              {"https://releases.example/info.json", "not the upgrade-info JSON"},
		"an unknown member":       {strings.Replace(validInfo(), `{"binaries"`, `{"note":"x","binaries"`, 1), "unknown field"},
		"trailing data":           {validInfo() + `{}`, "after the JSON object"},
		"no binaries":             {`{"verifiers":{"linux/amd64":"https://h/v?checksum=` + sum("b") + `"}}`, "binaries names no linux/amd64"},
		"no verifiers":            {`{"binaries":{"linux/amd64":"https://h/o?checksum=` + sum("a") + `"}}`, "verifiers names no linux/amd64"},
		"empty objects":           {`{"binaries":{},"verifiers":{}}`, "names no linux/amd64"},
		"only another platform":   {`{"binaries":{"darwin/arm64":"https://h/o?checksum=` + sum("a") + `"},"verifiers":{"linux/amd64":"https://h/v?checksum=` + sum("b") + `"}}`, "binaries names no linux/amd64"},
		"any is not a platform":   {`{"binaries":{"any":"https://h/o?checksum=` + sum("a") + `","linux/amd64":"https://h/o?checksum=` + sum("a") + `"},"verifiers":{"linux/amd64":"https://h/v?checksum=` + sum("b") + `"}}`, `platform "any"`},
		"http":                    {strings.Replace(validInfo(), "https://releases", "http://releases", 1), "https URL"},
		"credentials in the URL":  {strings.Replace(validInfo(), "https://releases", "https://user:pw@releases", 1), "no credentials"},
		"no checksum":             {strings.Replace(validInfo(), "?checksum=sha256:"+infoBinarySum, "", 1), "exactly one checksum"},
		"md5":                     {strings.Replace(validInfo(), "sha256:"+infoBinarySum, "md5:"+infoBinarySum[:32], 1), "exactly one checksum"},
		"a short digest":          {strings.Replace(validInfo(), infoVerifierSum, infoVerifierSum[:60], 1), "exactly one checksum"},
		"an uppercase digest":     {strings.Replace(validInfo(), infoBinarySum, strings.ToUpper(strings.Repeat("a", 64)), 1), "exactly one checksum"},
		"two checksums":           {strings.Replace(validInfo(), "?checksum=sha256:"+infoBinarySum, "?checksum=sha256:"+infoBinarySum+"&checksum=sha256:"+infoBinarySum, 1), "exactly one checksum"},
		"a fragment":              {strings.Replace(validInfo(), "sha256:"+infoBinarySum, "sha256:"+infoBinarySum+"#x", 1), "no fragment"},
		"a malformed platform":    {strings.Replace(validInfo(), `"binaries":{`, `"binaries":{"linux":"https://h/o?checksum=`+sum("a")+`",`, 1), `want os/arch`},
		"a URL over the bound":    {strings.Replace(validInfo(), "releases.example/oramad", "releases.example/"+strings.Repeat("p", MaxUpgradeURLBytes), 1), "1 to 512 bytes"},
		"info over the bound":     {validInfo() + strings.Repeat(" ", MaxUpgradeInfoBytes), "bytes of JSON"},
		"null members":            {`{"binaries":null,"verifiers":null}`, "names no linux/amd64"},
		"a string for a platform": {`{"binaries":{"linux/amd64":5},"verifiers":{}}`, "not the upgrade-info JSON"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseUpgradeInfo(tc.info)
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestParseUpgradeInfo_namesEveryProblem(t *testing.T) {
	_, err := ParseUpgradeInfo(`{"binaries":{},"verifiers":{}}`)
	require.ErrorContains(t, err, "binaries names no linux/amd64")
	require.ErrorContains(t, err, "verifiers names no linux/amd64")
}

func TestValidUpgradeName_isWhatCosmovisorCanStage(t *testing.T) {
	for _, ok := range []string{"v1", "v0.4.0", "a", "plan-2_x.3", strings.Repeat("a", 64)} {
		require.True(t, ValidUpgradeName(ok), ok)
	}
	for _, bad := range []string{"", "V2", "-v1", ".v1", "v 1", "v/1", "v1\n", strings.Repeat("a", 65)} {
		require.False(t, ValidUpgradeName(bad), "%q", bad)
	}
}

func TestSoftwareUpgrade_contentNeedsAValidInfo(t *testing.T) {
	good := ProposalContent{SoftwareUpgrade: &SoftwareUpgrade{Name: "v2", Height: 10, Info: validInfo()}}
	require.NoError(t, good.ValidateBasic())

	noInfo := ProposalContent{SoftwareUpgrade: &SoftwareUpgrade{Name: "v2", Height: 10}}
	require.ErrorContains(t, noInfo.ValidateBasic(), "software upgrade info")

	upper := ProposalContent{SoftwareUpgrade: &SoftwareUpgrade{Name: "V2", Height: 10, Info: validInfo()}}
	require.ErrorContains(t, upper.ValidateBasic(), "plan name")

	badSum := ProposalContent{SoftwareUpgrade: &SoftwareUpgrade{Name: "v2", Height: 10, Info: strings.Replace(validInfo(), infoBinarySum, "zz", 1)}}
	require.ErrorContains(t, badSum.ValidateBasic(), "checksum")
}
