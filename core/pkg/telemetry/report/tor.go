package report

import (
	"context"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

// torUnit is the node's client-only Tor daemon.
const torUnit = "orama-namespace-tor@index"

// torBootstrapUnknown is BootstrapPct when the running Tor process has no
// "Bootstrapped" line left in its journal.
const torBootstrapUnknown = -1

// legacyAnyonePaths are files the removed Anyone network leaves behind until
// `orama node upgrade` cleans them.
var legacyAnyonePaths = []string{"/etc/anon", "/var/lib/anon", "/etc/apt/sources.list.d/anon.list"}

var torBootstrapRe = regexp.MustCompile(`Bootstrapped\s+(\d+)%`)

// collectTor gathers the Tor client's health.
func collectTor() *TorReport {
	ctx := context.Background()
	r := &TorReport{BootstrapPct: torBootstrapUnknown}

	if out, err := runCmd(ctx, "systemctl", "is-active", torUnit); err == nil {
		r.ClientActive = strings.TrimSpace(out) == "active"
	}
	if out, err := runCmd(ctx, "ss", "-tln"); err == nil {
		r.SocksListening = portIsListening(out, constants.TorSOCKSPort)
	}
	// Bootstrap of the process running now: its own invocation's journal only.
	if inv, err := runCmd(ctx, "systemctl", "show", "-p", "InvocationID", "--value", torUnit); err == nil && strings.TrimSpace(inv) != "" {
		if out, err := runCmd(ctx, "journalctl", "--no-pager", "-o", "cat", "_SYSTEMD_INVOCATION_ID="+strings.TrimSpace(inv)); err == nil {
			r.BootstrapPct = parseTorBootstrap(out)
		}
	}
	r.Bootstrapped = r.BootstrapPct == 100
	r.LegacyAnyone = legacyAnyonePresent()
	return r
}

// parseTorBootstrap returns the percentage of the last "Bootstrapped N%" line
// in Tor's log output, or torBootstrapUnknown if there is none.
func parseTorBootstrap(log string) int {
	pct := torBootstrapUnknown
	for _, m := range torBootstrapRe.FindAllStringSubmatch(log, -1) {
		if v, err := strconv.Atoi(m[1]); err == nil {
			pct = v
		}
	}
	return pct
}

func legacyAnyonePresent() bool {
	if out, err := runCmd(context.Background(), "systemctl", "is-active", "orama-namespace-anyone-client@index"); err == nil && strings.TrimSpace(out) == "active" {
		return true
	}
	for _, p := range legacyAnyonePaths {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}
