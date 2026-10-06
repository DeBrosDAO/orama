package clusterguide

import (
	"fmt"
	"strings"
	"time"
)

// The guide's example values. Fixture.Bind replaces them with the machines
// the run was given.
const (
	guideGenesisIP  = "203.0.113.10"
	guideSecondIP   = "203.0.113.11"
	guideThirdIP    = "203.0.113.12"
	guideDomain     = "cluster.example.com"
	guideEnv        = "mycluster"
	guideArchive    = "/tmp/orama-linux-amd64.tar.gz"
	guideTokenFile  = "/path/to/token"
	guideSiteSource = "./site"
)

// The guide's sections the harness runs.
const (
	SectionInstall = "Install"
	SectionUse     = "Use it"
	SectionCheck   = "Check it"
)

// MinNodes is the machines the guide's three-voter cluster needs.
const MinNodes = 3

// Fixture is what a run is given: machines, a build, and a domain.
type Fixture struct {
	// IPs are the machines: the genesis nameserver first, then the joiners.
	IPs        []string
	BaseDomain string
	// EnvName replaces the guide's environment name, so a run never touches an
	// environment the operator already has.
	EnvName string
	Archive string
	// TokenFile is a Cloudflare token for the parent zone. Empty leaves the
	// guide's token step out: the delegation must already exist.
	TokenFile string
	SiteDir   string
	// UseOnly runs only the "Use it" and "Check it" sections, against a cluster
	// that is already up (an `orama sandbox create` cluster).
	UseOnly bool

	// DelegationWait bounds the wait for the NS records and the genesis
	// certificate the guide says to wait for.
	DelegationWait time.Duration

	// HostKey returns the SSH host-key fingerprint of a machine, for
	// --host-key. The guide leaves it to a reader to confirm interactively.
	HostKey func(ip string) (string, error)
	// LookupNS returns the nameservers the internet has for a domain.
	LookupNS func(domain string) ([]string, error)
	// CertServed checks the domain serves a certificate on 443.
	CertServed func(domain string) error
	// Sleep is time.Sleep, replaced in tests.
	Sleep func(time.Duration)
}

// Validate refuses a fixture that cannot run the guide.
func (f *Fixture) Validate() error {
	if !f.UseOnly && len(f.IPs) < MinNodes {
		return fmt.Errorf("the guide installs %d machines; the fixture has %d", MinNodes, len(f.IPs))
	}
	if strings.TrimSpace(f.BaseDomain) == "" {
		return fmt.Errorf("no base domain")
	}
	if !f.UseOnly && strings.TrimSpace(f.Archive) == "" {
		return fmt.Errorf("no build archive")
	}
	if strings.TrimSpace(f.EnvName) == "" {
		return fmt.Errorf("no environment name")
	}
	if strings.TrimSpace(f.SiteDir) == "" {
		return fmt.Errorf("no directory to deploy")
	}
	return nil
}

// Bind replaces the guide's example values in a command's arguments with the fixture's, and
// fails when an example value is left over, so a new example in the guide is
// noticed instead of being sent to a real machine.
func (f *Fixture) Bind(c Command) ([]string, error) {
	pairs := []string{
		guideDomain, f.BaseDomain,
		guideEnv, f.EnvName,
		guideArchive, f.Archive,
		guideTokenFile, f.TokenFile,
		guideSiteSource, f.SiteDir,
	}
	for i, guide := range []string{guideGenesisIP, guideSecondIP, guideThirdIP} {
		ip := guide
		if i < len(f.IPs) {
			ip = f.IPs[i]
		}
		pairs = append(pairs, guide, ip)
	}
	// A cluster that is already up has no machines to bind the Install
	// section to; that section is skipped, so its examples may stay.
	enforce := !(f.UseOnly && c.Section == SectionInstall)
	out := make([]string, len(c.Argv))
	for i, a := range c.Argv {
		out[i] = strings.NewReplacer(pairs...).Replace(a)
		if left := exampleValue.FindString(out[i]); left != "" && enforce {
			return nil, fmt.Errorf("argument %q still holds the example value %q: bind it in fixture.go", a, left)
		}
	}
	return out, nil
}
