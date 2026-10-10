package tornet

import (
	"errors"
	"fmt"
	"strings"
)

// exitBlockMarkers are the lines that start the exit section RelayTorrc
// writes last: the explanatory comment of an exit, and the first directive of
// either form.
const (
	exitCommentPrefix = "# Exit:"
	exitDirective     = "ExitRelay "
)

// SetExit returns the torrc of a relay with its exit section replaced: an exit
// under the policy ExitPolicyLines builds from reject when exit is true, a
// non-exit otherwise. Everything before the exit section (the relay's
// nickname, address, contact, bandwidth and family) is kept byte for byte, so
// switching the role changes nothing about the relay's identity. It refuses an
// exit on a network whose file does not allow one, a directory authority (it
// never exits), and a torrc that has no exit section.
func SetExit(existing string, exit bool, reject []string, network Network) (string, error) {
	if strings.Contains(existing, "AuthoritativeDirectory") {
		return "", errors.New("this torrc is a directory authority's, which never exits")
	}
	cfg := RelayConfig{Exit: exit, ExitReject: reject, Network: network}
	if exit && !network.AllowExit {
		return "", fmt.Errorf("network %s does not allow exits: its network file has allow_exit false", network.Name)
	}
	if !exit && len(reject) > 0 {
		return "", errors.New("an exit reject list needs the exit role")
	}
	if exit {
		if err := checkExitSummary(ExitPolicyLines(reject)); err != nil {
			return "", fmt.Errorf("exit policy: %w", err)
		}
	}
	for _, r := range reject {
		if _, err := exitRejectRule(r); err != nil {
			return "", fmt.Errorf("exit reject rule %q: %w", r, err)
		}
	}
	head, err := beforeExitSection(existing)
	if err != nil {
		return "", err
	}
	var b torrc
	b.WriteString(head)
	b.exit(cfg)
	return b.String(), nil
}

// beforeExitSection is the torrc up to its exit section.
func beforeExitSection(torrcText string) (string, error) {
	lines := strings.SplitAfter(torrcText, "\n")
	start := -1
	for i, l := range lines {
		if strings.HasPrefix(l, exitDirective) {
			start = i
			break
		}
	}
	if start < 0 {
		return "", errors.New("this torrc has no ExitRelay line, so it is not a relay's torrc written by `orama maint global install`")
	}
	if start > 0 && strings.HasPrefix(lines[start-1], exitCommentPrefix) {
		start--
	}
	return strings.Join(lines[:start], ""), nil
}
