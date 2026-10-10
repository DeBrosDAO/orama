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
	guideTokenFile  = "/path/to/token"
	guideSiteSource = "./site"

	// The release flags of the Install section. Bind replaces their values
	// with the fixture's release, or drops them for the fixture's archive.
	guideRelease     = "0.3.1"
	guideReleaseRepo = "https://releases.example.org/tuf"
	guideReleaseRoot = "./root.json"
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
	// Archive is a build archive to install in place of a release. Release,
	// ReleaseRepo and ReleaseRoot are a published release to install. A fixture
	// has one or the other.
	Archive     string
	Release     string
	ReleaseRepo string
	ReleaseRoot string
	// TokenFile is a Cloudflare token for the parent zone. Empty leaves the
	// guide's token step out: the delegation must already exist.
	TokenFile string
	SiteDir   string
	// UseOnly runs only the "Use it" and "Check it" sections, against a cluster
	// that is already up (an `orama maint sandbox create` cluster).
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
	if !f.UseOnly {
		if err := f.validateSource(); err != nil {
			return err
		}
	}
	if strings.TrimSpace(f.EnvName) == "" {
		return fmt.Errorf("no environment name")
	}
	if strings.TrimSpace(f.SiteDir) == "" {
		return fmt.Errorf("no directory to deploy")
	}
	return nil
}

// validateSource requires exactly one thing to install: an archive, or a
// release with its repository and root.
func (f *Fixture) validateSource() error {
	release := f.Release != "" || f.ReleaseRepo != "" || f.ReleaseRoot != ""
	switch {
	case release && f.Archive != "":
		return fmt.Errorf("a build archive and a release are alternatives; give one")
	case release && (f.Release == "" || f.ReleaseRepo == "" || f.ReleaseRoot == ""):
		return fmt.Errorf("a release needs its version, its repository and its root")
	case !release && strings.TrimSpace(f.Archive) == "":
		return fmt.Errorf("nothing to install: give a build archive, or a release with its repository and root")
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
	}
	out = f.bindSource(out)
	for i, a := range out {
		if left := exampleValue.FindString(a); left != "" && enforce {
			return nil, fmt.Errorf("argument %q still holds the example value %q: bind it in fixture.go", c.Argv[min(i, len(c.Argv)-1)], left)
		}
	}
	return out, nil
}

// releaseFlags are the flags that name a release in the guide's setup commands.
var releaseFlags = map[string]string{
	"--release":      guideRelease,
	"--release-repo": guideReleaseRepo,
	"--release-root": guideReleaseRoot,
}

// bindSource gives a setup command the fixture's way to install: the release
// the fixture was given, or, when it was given an archive, --archive in place
// of the three release flags.
func (f *Fixture) bindSource(argv []string) []string {
	if f.Release == "" && f.Archive == "" {
		return argv
	}
	fixture := map[string]string{"--release": f.Release, "--release-repo": f.ReleaseRepo, "--release-root": f.ReleaseRoot}
	out := make([]string, 0, len(argv))
	dropped := false
	for i := 0; i < len(argv); i++ {
		if _, ok := releaseFlags[argv[i]]; !ok || i+1 >= len(argv) {
			out = append(out, argv[i])
			continue
		}
		if f.Release != "" {
			out = append(out, argv[i], fixture[argv[i]])
		} else {
			dropped = true
		}
		i++
	}
	if dropped {
		out = append(out, "--archive", f.Archive)
	}
	return out
}
