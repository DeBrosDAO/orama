package setup

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Enrollment is the one connection that bootstraps every later trust
// relationship with a node. It carries the VPS password and installs the key
// that all subsequent access depends on. Accepting whatever host key answers
// makes that moment unauthenticated: an on-path attacker collects the password
// and installs a key of their own, and every "secure" connection afterwards is
// secure to them.
//
// So the host key is pinned before the password is sent. The operator either
// supplies the expected fingerprint up front (--host-key, for unattended runs)
// or confirms the scanned one against what their provider's console shows.

// hostKey is a scanned SSH host key for one address.
type hostKey struct {
	// lines are known_hosts entries exactly as ssh-keyscan produced them.
	lines []string
	// fingerprints are the SHA256 fingerprints of those entries, in the same
	// form ssh-keygen prints (e.g. "SHA256:abc...").
	fingerprints []string
}

// scanHostKey retrieves the host keys presented by ip and their fingerprints.
func scanHostKey(ip string) (*hostKey, error) {
	keyscan, err := findBinary("ssh-keyscan")
	if err != nil {
		return nil, fmt.Errorf("ssh-keyscan is required to pin the host key: %w", err)
	}

	out, err := runCommand(keyscan, "-T", "10", "-t", "ed25519,ecdsa,rsa", ip)
	if err != nil {
		return nil, fmt.Errorf("ssh-keyscan %s failed: %w (%s)", ip, err, strings.TrimSpace(out))
	}

	var lines []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return nil, fmt.Errorf("no SSH host key returned by %s — is the host reachable on port 22?", ip)
	}

	hk := &hostKey{lines: lines}
	hk.fingerprints, err = fingerprintLines(lines)
	if err != nil {
		return nil, err
	}
	return hk, nil
}

// ScanHostKeys returns the known_hosts entries ip presents now. Nothing
// vouches for them: the caller decides what trusting them means.
func ScanHostKeys(ip string) ([]string, error) {
	hk, err := scanHostKey(ip)
	if err != nil {
		return nil, err
	}
	return hk.lines, nil
}

// fingerprintLines runs the known_hosts entries through ssh-keygen -l.
func fingerprintLines(lines []string) ([]string, error) {
	keygen, err := findBinary("ssh-keygen")
	if err != nil {
		return nil, fmt.Errorf("ssh-keygen is required to fingerprint the host key: %w", err)
	}

	tmp, err := os.CreateTemp("", "orama-hostkey-*")
	if err != nil {
		return nil, fmt.Errorf("create temp file for host key: %w", err)
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.WriteString(strings.Join(lines, "\n") + "\n"); err != nil {
		tmp.Close()
		return nil, fmt.Errorf("write temp host key: %w", err)
	}
	tmp.Close()

	out, err := runCommand(keygen, "-l", "-f", tmp.Name())
	if err != nil {
		return nil, fmt.Errorf("fingerprint host key: %w (%s)", err, strings.TrimSpace(out))
	}

	var fps []string
	for _, line := range strings.Split(out, "\n") {
		// "256 SHA256:abc... 1.2.3.4 (ED25519)"
		for _, field := range strings.Fields(line) {
			if strings.HasPrefix(field, "SHA256:") {
				fps = append(fps, field)
				break
			}
		}
	}
	// fingerprints[i] must describe lines[i]: trusting a key is done by line.
	if len(fps) != len(lines) {
		return nil, fmt.Errorf("ssh-keygen fingerprinted %d of %d host keys: %s", len(fps), len(lines), strings.TrimSpace(out))
	}
	return fps, nil
}

// matching returns the scanned entries whose fingerprint is want. The
// comparison accepts the fingerprint with or without its "SHA256:" prefix,
// since operators copy it from consoles that print it either way.
//
// Only these entries may be trusted. A scan returns every key the answering
// host offers, so trusting all of them once one matched let an on-path
// attacker pass the pin by offering the real key alongside its own.
func (h *hostKey) matching(want string) []string {
	want = strings.TrimPrefix(strings.TrimSpace(want), "SHA256:")
	if want == "" {
		return nil
	}
	var lines []string
	for i, fp := range h.fingerprints {
		if strings.TrimPrefix(fp, "SHA256:") == want {
			lines = append(lines, h.lines[i])
		}
	}
	return lines
}

// matches reports whether want names one of the scanned keys.
func (h *hostKey) matches(want string) bool {
	return len(h.matching(want)) > 0
}

// writeKnownHosts writes the trusted entries to a file for ssh to verify
// against, and returns its path. The caller removes the containing directory.
func writeKnownHosts(dir string, lines []string) (string, error) {
	path := filepath.Join(dir, "known_hosts")
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		return "", fmt.Errorf("write known_hosts: %w", err)
	}
	return path, nil
}

// confirmHostKey settles whether the scanned key is the one the operator
// expects, either by matching a fingerprint they supplied or by asking, and
// returns the known_hosts entries to trust: only the matched key for a pin,
// the keys the operator was shown for an interactive confirmation.
func confirmHostKey(hk *hostKey, ip, expected string, in io.Reader, out io.Writer) ([]string, error) {
	if strings.TrimSpace(expected) != "" {
		trusted := hk.matching(expected)
		if len(trusted) == 0 {
			return nil, fmt.Errorf(
				"host key fingerprint mismatch for %s:\n  expected: %s\n  offered:  %s\nrefusing to use your credential on it",
				ip, expected, strings.Join(hk.fingerprints, ", "))
		}
		fmt.Fprintf(out, "  Host key matches --host-key\n")
		return trusted, nil
	}

	fmt.Fprintf(out, "\n  The VPS at %s presents this SSH host key:\n", ip)
	for _, fp := range hk.fingerprints {
		fmt.Fprintf(out, "    %s\n", fp)
	}
	fmt.Fprintf(out, "  Check it against your provider's console before continuing —\n")
	fmt.Fprintf(out, "  your existing credential for it is used over this connection.\n")
	fmt.Fprintf(out, "  Continue? [y/N]: ")

	reader := bufio.NewReader(in)
	answer, err := reader.ReadString('\n')
	if err != nil && answer == "" {
		return nil, fmt.Errorf("could not read confirmation: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return hk.lines, nil
	default:
		return nil, fmt.Errorf("host key not confirmed for %s; re-run with --host-key <fingerprint> to pin it non-interactively", ip)
	}
}

// runCommandWithEnvStdin runs bin with extra environment entries and the given
// stdin.
//
// Both exist to keep secrets out of argv: a password on the command line is
// readable by any local process through ps, and data piped on stdin needs no
// shell quoting, so it cannot break out of the remote command.
func runCommandWithEnvStdin(bin string, env []string, stdin string, args ...string) (string, error) {
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
