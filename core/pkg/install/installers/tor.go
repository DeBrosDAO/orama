package installers

import (
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

// Tor is installed from the Tor Project's own apt repository, as the Tor
// Project recommends: Ubuntu's universe package has not reliably received
// security updates. The node runs it client-only, as the Orama-managed unit
// orama-namespace-tor@index with an Orama-written torrc; the distro's own
// tor.service / tor@default.service are masked so nothing else binds the
// SOCKS port.
const (
	// TorArchiveKeyFingerprint is the Tor Project's deb.torproject.org archive
	// signing key. The downloaded key is refused unless its primary key
	// carries exactly this fingerprint.
	TorArchiveKeyFingerprint = "A3C4F0F979CAA22CDBA8F512EE8CBC9E886DDD89"
	torArchiveKeyURL         = "https://deb.torproject.org/torproject.org/" + TorArchiveKeyFingerprint + ".asc"
	torArchiveURI            = "https://deb.torproject.org/torproject.org/"

	// TorKeyringPath is where both this installer and the
	// deb.torproject.org-keyring package keep the archive key, so the package
	// rotates it in place.
	TorKeyringPath = "/usr/share/keyrings/deb.torproject.org-keyring.gpg"
	// TorAptSourcePath is the deb822 source this installer writes.
	TorAptSourcePath = "/etc/apt/sources.list.d/tor.sources"

	// TorDataDir is the Tor client's DataDirectory. The unit creates it with
	// StateDirectory=orama-tor, owned by debian-tor with mode 0700 as Tor
	// requires.
	TorDataDir = "/var/lib/orama-tor"

	torBinaryPath      = "/usr/bin/tor"
	torKeyDownloadMax  = 256 * 1024
	torKeyFetchTimeout = 60 * time.Second
)

// TorAptPackages are installed from deb.torproject.org. The keyring package
// keeps the archive key current.
var TorAptPackages = []string{"tor", TorKeyringPackage}

// TorKeyringPackage maintains the archive key at TorKeyringPath.
const TorKeyringPackage = "deb.torproject.org-keyring"

// TorDistroUnits are the units the tor package ships and would start on its
// own, binding 127.0.0.1:9050 with /etc/tor/torrc. They are masked before the
// package is installed, so they never race the Orama unit for the port.
var TorDistroUnits = []string{"tor.service", "tor@default.service"}

// torSuites maps an OS codename to its deb.torproject.org suite. The Tor
// Project publishes packages for these releases only; any other codename is
// refused with an error naming them rather than pointed at a suite that
// does not exist.
var torSuites = map[string]string{
	"bookworm": "bookworm",
	"trixie":   "trixie",
	"jammy":    "jammy",
	"noble":    "noble",
	"resolute": "resolute",
}

// TorSuiteFor returns the deb.torproject.org suite for an OS codename.
func TorSuiteFor(codename string) (string, error) {
	suite, ok := torSuites[codename]
	if !ok {
		return "", fmt.Errorf("the Tor Project publishes no apt suite for OS codename %q (supported: bookworm, trixie, jammy, noble, resolute)", codename)
	}
	return suite, nil
}

// TorAptSource renders the deb822 source for suite.
func TorAptSource(suite string) string {
	return fmt.Sprintf(`# Managed by Orama Network. Tor Project repository for the node's Tor client.
Types: deb
URIs: %s
Suites: %s
Components: main
Signed-By: %s
`, torArchiveURI, suite, TorKeyringPath)
}

// GenerateTorrc renders the Orama torrc: a client and nothing else.
func GenerateTorrc() string {
	return fmt.Sprintf(`# Managed by Orama Network — rewritten on every install and upgrade.
# Client only. The gateway's /v1/proxy/anon, /v1/proxy/tunnel and the
# anon_fetch WASM host function dial this SOCKS port. This node relays nothing.

# Loopback only. IsolateSOCKSAuth (Tor's default, stated here because the
# tunnel depends on it) gives each distinct SOCKS username its own circuit.
SocksPort %s IsolateSOCKSAuth
ClientOnly 1
ORPort 0
DirPort 0
ExitRelay 0
# Never open a stream to a private or local address, including through a
# redirect a destination sends back.
ClientRejectInternalAddresses 1

DataDirectory %s
Log notice stdout
`, constants.TorSOCKSAddr(), TorDataDir)
}
