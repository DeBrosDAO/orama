// Package planinfo reads the checksums a governed chain upgrade plan names. A SoftwareUpgrade
// proposal (chain x/houses) carries cosmovisor's upgrade-info JSON, extended with the shielded
// verifier, and x/upgrade holds it as the plan's info:
//
//	{"binaries":  {"linux/amd64": "https://host/oramad?checksum=sha256:<64 hex>"},
//	 "verifiers": {"linux/amd64": "https://host/orama-orchard-verifier?checksum=sha256:<64 hex>"}}
//
// The chain checks the shape when the proposal is submitted; a node checks the files it stages
// against the digests here.
package planinfo

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// ErrMismatch is what Check wraps: the oramad or verifier offered is not the one the plan names.
var ErrMismatch = errors.New("not the binary the upgrade plan names")

const (
	// checksumParam and checksumPrefix are how a binary's URL names its digest, the way cosmovisor
	// reads it: "...?checksum=sha256:<hex>".
	checksumParam  = "checksum"
	checksumPrefix = "sha256:"
)

var (
	// checksumValue is the one value the checksum parameter may have: the prefix and 64 lowercase hex digits.
	checksumValue = regexp.MustCompile(`^` + checksumPrefix + `[0-9a-f]{64}$`)
	// sha256HexPattern is a digest the way Check compares it: 64 lowercase hex digits.
	sha256HexPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Digests are the sha256 digests (lowercase hex) a plan names for the chain binary and the
// shielded verifier of one platform.
type Digests struct {
	Oramad, Verifier string
}

// Parse reads the digests info names for osArch ("linux/amd64"). Only the platform's own
// entries count; there is no "any" fallback, because the proposal had to name every required
// platform. An info that is not that JSON, or names no checksum for either file, is an error: a
// plan whose binaries cannot be checked is not one to stage for. The JSON is read the way the chain
// reads it (ParseUpgradeInfo in chain/x/houses/types): an unknown member or data after the object is
// an error. name is the plan's, for the message.
func Parse(name, info, osArch string) (Digests, error) {
	var doc struct {
		Binaries  map[string]string `json:"binaries"`
		Verifiers map[string]string `json:"verifiers"`
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(info)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return Digests{}, fmt.Errorf("the scheduled upgrade %q carries no upgrade-info JSON naming its binaries' checksums (%w)", name, err)
	}
	if dec.More() {
		return Digests{}, fmt.Errorf("the scheduled upgrade %q carries upgrade-info JSON with data after the JSON object", name)
	}
	d := Digests{Oramad: urlChecksum(doc.Binaries[osArch]), Verifier: urlChecksum(doc.Verifiers[osArch])}
	switch {
	case d.Oramad == "":
		return Digests{}, fmt.Errorf("the scheduled upgrade %q names no oramad checksum for %s", name, osArch)
	case d.Verifier == "":
		return Digests{}, fmt.Errorf("the scheduled upgrade %q names no orama-orchard-verifier checksum for %s", name, osArch)
	}
	return d, nil
}

// urlChecksum is the sha256 (lowercase hex) a binary's URL names, or "". It reads the URL exactly as
// the chain validated it: url.Parse, url.ParseQuery, and exactly one lowercase
// "checksum=sha256:<64 lowercase hex>" query value. It is a copy of upgradeURLChecksum in
// chain/x/houses/types/upgrade_info.go (core cannot import the chain), and TestURLChecksum_isTheChainsFunction
// fails when the two differ.
func urlChecksum(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return ""
	}
	sums := query[checksumParam]
	if len(sums) != 1 || !checksumValue.MatchString(sums[0]) {
		return ""
	}
	return strings.TrimPrefix(sums[0], checksumPrefix)
}

// Valid reports whether both digests are well formed.
func (d Digests) Valid() bool {
	return sha256HexPattern.MatchString(d.Oramad) && sha256HexPattern.MatchString(d.Verifier)
}

// Check refuses an oramad or a verifier whose sha256 is not the one the plan named. plan is the
// plan's name, for the message.
func (d Digests) Check(plan, oramadSHA256, verifierSHA256 string) error {
	if !d.Valid() {
		return fmt.Errorf("the upgrade %q names no usable checksum for the oramad and the verifier of this platform, so nothing is staged for it: %w", plan, ErrMismatch)
	}
	if oramadSHA256 != d.Oramad {
		return fmt.Errorf("the upgrade %q names an oramad with sha256 %s, but this oramad is %s: it is not the binary the proposal was for, so it is not staged: %w",
			plan, Short(d.Oramad), Short(oramadSHA256), ErrMismatch)
	}
	if verifierSHA256 != d.Verifier {
		return fmt.Errorf("the upgrade %q names an orama-orchard-verifier with sha256 %s, but this verifier is %s: it is not the one the proposal was for, so it is not staged: %w",
			plan, Short(d.Verifier), Short(verifierSHA256), ErrMismatch)
	}
	return nil
}

// CheckOramad is Check for the oramad alone. Staging calls it with the digest of the staged copy
// itself, so what is checked is what is installed.
func (d Digests) CheckOramad(plan, oramadSHA256 string) error {
	return d.Check(plan, oramadSHA256, d.Verifier)
}

// CheckVerifier is Check for the shielded verifier alone.
func (d Digests) CheckVerifier(plan, verifierSHA256 string) error {
	return d.Check(plan, d.Oramad, verifierSHA256)
}

// Short is the first digits of a digest, enough to tell two binaries apart.
func Short(sum string) string {
	const digits = 12
	if len(sum) > digits {
		return sum[:digits]
	}
	return sum
}
