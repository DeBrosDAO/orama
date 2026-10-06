package fleet

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/DeBrosOfficial/network/e2e/harness/sshx"
)

// sshPort is the port the known_hosts entries are pinned for.
const sshPort = "22"

// HostKeyFingerprint returns the SHA256:... fingerprint of the host key the
// provisioner pinned for n in the run's known_hosts file: the value
// `orama node setup --host-key` takes. It fails when no key, or more than one
// distinct key, is pinned for n's address.
func HostKeyFingerprint(st *State, n Node) (string, error) {
	if net.ParseIP(n.PublicIP) == nil {
		return "", fmt.Errorf("%s has no valid public IP (%q) to look its host key up by", n.Name, n.PublicIP)
	}
	raw, err := os.ReadFile(st.KnownHostsFile)
	if err != nil {
		return "", fmt.Errorf("failed to read the run's known_hosts %s: %w", st.KnownHostsFile, err)
	}
	want := knownhosts.Normalize(net.JoinHostPort(n.PublicIP, sshPort))
	var found []string
	for rest := raw; len(bytes.TrimSpace(rest)) > 0; {
		_, hosts, key, _, next, err := ssh.ParseKnownHosts(rest)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("failed to parse known_hosts %s: %w", st.KnownHostsFile, err)
		}
		rest = next
		for _, h := range hosts {
			if fp := sshx.Fingerprint(key); h == want && !contains(found, fp) {
				found = append(found, fp)
			}
		}
	}
	switch len(found) {
	case 0:
		return "", fmt.Errorf("no host key pinned for %s (%s) in %s", n.Name, n.PublicIP, st.KnownHostsFile)
	case 1:
		return found[0], nil
	}
	return "", fmt.Errorf("%d different host keys pinned for %s (%s) in %s: the pin is ambiguous", len(found), n.Name, n.PublicIP, st.KnownHostsFile)
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
