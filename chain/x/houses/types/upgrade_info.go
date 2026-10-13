package types

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

const (
	// UpgradeChecksumPrefix is how cosmovisor's upgrade-info names a binary's digest in its URL:
	// "...?checksum=sha256:<hex>".
	UpgradeChecksumPrefix = "sha256:"

	// MaxUpgradeInfoBytes bounds SoftwareUpgrade.Info: two URLs per required platform fit with
	// room to spare.
	MaxUpgradeInfoBytes = 4096

	// MaxUpgradeURLBytes bounds one binary URL.
	MaxUpgradeURLBytes = 512

	upgradeChecksumParam = "checksum"
)

// RequiredUpgradePlatforms are the platforms every upgrade must name a chain binary and a
// shielded verifier for: the platforms a release is built for (chain/Makefile
// build-linux-amd64-*).
var RequiredUpgradePlatforms = []string{"linux/amd64"}

var (
	upgradeNamePattern     = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	upgradePlatformPattern = regexp.MustCompile(`^[a-z0-9]+/[a-z0-9]+$`)
	upgradeChecksumValue   = regexp.MustCompile(`^` + UpgradeChecksumPrefix + `[0-9a-f]{64}$`)
)

// ValidUpgradeName reports whether name is an upgrade plan name: lowercase letters, digits, '.',
// '_' and '-', 1 to 64 characters, starting with a letter or digit. It is the name cosmovisor
// stages a binary under (upgrades/<name>/bin), which is why uppercase is refused.
func ValidUpgradeName(name string) bool { return upgradeNamePattern.MatchString(name) }

// UpgradeInfo is the JSON SoftwareUpgrade.Info carries: cosmovisor's upgrade-info ("binaries",
// platform to download URL with a checksum) and the same for the shielded verifier that must run
// beside the chain binary ("verifiers").
type UpgradeInfo struct {
	Binaries  map[string]string `json:"binaries"`
	Verifiers map[string]string `json:"verifiers"`
}

// ParseUpgradeInfo decodes and validates a SoftwareUpgrade.Info: it is one JSON object with only
// the two members, each naming every required platform with an https URL that carries exactly one
// "checksum=sha256:<64 lowercase hex>" query value. The error names every problem found.
func ParseUpgradeInfo(info string) (UpgradeInfo, error) {
	var out UpgradeInfo
	if len(info) == 0 || len(info) > MaxUpgradeInfoBytes {
		return out, fmt.Errorf("software upgrade info must be 1 to %d bytes of JSON, got %d", MaxUpgradeInfoBytes, len(info))
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(info)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return UpgradeInfo{}, fmt.Errorf("software upgrade info is not the upgrade-info JSON: %w", err)
	}
	if dec.More() {
		return UpgradeInfo{}, fmt.Errorf("software upgrade info has data after the JSON object")
	}
	var problems []string
	problems = append(problems, checkUpgradeURLs("binaries", out.Binaries)...)
	problems = append(problems, checkUpgradeURLs("verifiers", out.Verifiers)...)
	if len(problems) > 0 {
		return UpgradeInfo{}, fmt.Errorf("software upgrade info: %s", strings.Join(problems, "; "))
	}
	return out, nil
}

// BinaryChecksum is the sha256 (lowercase hex) the info names for the chain binary of platform,
// or "" when it names none.
func (i UpgradeInfo) BinaryChecksum(platform string) string {
	return upgradeURLChecksum(i.Binaries[platform])
}

// VerifierChecksum is the sha256 (lowercase hex) the info names for the shielded verifier of
// platform, or "" when it names none.
func (i UpgradeInfo) VerifierChecksum(platform string) string {
	return upgradeURLChecksum(i.Verifiers[platform])
}

// upgradeURLChecksum is the sha256 (lowercase hex) a binary URL names, or "". It reads the URL the
// way checkUpgradeURL does: url.Parse, url.ParseQuery, and exactly one lowercase
// "checksum=sha256:<64 lowercase hex>" query value. core/pkg/planinfo carries a copy of this function
// that a node stages by; planinfo's sync test fails when the two differ, so keep them identical.
func upgradeURLChecksum(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return ""
	}
	sums := query[upgradeChecksumParam]
	if len(sums) != 1 || !upgradeChecksumValue.MatchString(sums[0]) {
		return ""
	}
	return strings.TrimPrefix(sums[0], UpgradeChecksumPrefix)
}

func checkUpgradeURLs(member string, urls map[string]string) []string {
	var problems []string
	for _, platform := range RequiredUpgradePlatforms {
		if _, ok := urls[platform]; !ok {
			problems = append(problems, fmt.Sprintf("%s names no %s", member, platform))
		}
	}
	platforms := make([]string, 0, len(urls))
	for platform := range urls { // nosemgrep: consensus-map-iteration -- keys are sorted before use
		platforms = append(platforms, platform)
	}
	sort.Strings(platforms)
	for _, platform := range platforms {
		if !upgradePlatformPattern.MatchString(platform) {
			problems = append(problems, fmt.Sprintf("%s has the platform %q, want os/arch", member, platform))
			continue
		}
		if err := checkUpgradeURL(urls[platform]); err != nil {
			problems = append(problems, fmt.Sprintf("%s[%s]: %v", member, platform, err))
		}
	}
	return problems
}

func checkUpgradeURL(raw string) error {
	if len(raw) == 0 || len(raw) > MaxUpgradeURLBytes {
		return fmt.Errorf("the URL must be 1 to %d bytes", MaxUpgradeURLBytes)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("not a URL: %w", err)
	}
	if u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" {
		return fmt.Errorf("must be an https URL with a host, no credentials and no fragment")
	}
	// "checksum=" before the query is a decoy: a reader that scans the raw string for it, as a
	// regular expression does, would take it for the digest.
	if beforeQuery, _, _ := strings.Cut(raw, "?"); strings.Contains(strings.ToLower(beforeQuery), upgradeChecksumParam+"=") {
		return fmt.Errorf("must not contain %q outside the query", upgradeChecksumParam+"=")
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return fmt.Errorf("the query is malformed: %w", err)
	}
	sums := query[upgradeChecksumParam]
	if len(sums) != 1 || !upgradeChecksumValue.MatchString(sums[0]) {
		return fmt.Errorf("must carry exactly one %s=%s<64 lowercase hex> query value", upgradeChecksumParam, UpgradeChecksumPrefix)
	}
	return nil
}
