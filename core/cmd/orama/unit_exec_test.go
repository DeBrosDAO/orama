package main

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/cmdmeta"
	"github.com/DeBrosOfficial/network/pkg/install"
)

// renderedGlobalUnits are the units `orama global install` writes, as it writes them.
func renderedGlobalUnits() map[string]string {
	return map[string]string{
		"orama-global-chain.service":           install.RenderGlobalChainUnit("id@203.0.113.10:31000"),
		"orama-global-ipfs.service":            install.RenderGlobalIPFSUnit(),
		"orama-global-ipfs-gc.service":         install.RenderGlobalIPFSGCUnit("198.18.0.2"),
		"orama-global-ipfs-gc.timer":           install.RenderGlobalIPFSGCTimer(),
		"orama-global-provider.service":        install.RenderGlobalProviderUnit("198.18.0.2"),
		"orama-global-archiver.service":        install.RenderGlobalArchiverUnit(),
		"orama-global-indexer.service":         install.RenderGlobalIndexerUnit(),
		"orama-global-repair.service":          install.RenderGlobalRepairUnit(),
		"orama-global-reporter.service":        install.RenderGlobalReporterUnit(),
		"orama-global-txgate.service":          install.RenderGlobalTxGateUnit(),
		"orama-global-tor-relay.service":       install.RenderGlobalTorRelayUnit(),
		"orama-global-tor-dirauth.service":     install.RenderGlobalTorDirauthUnit(),
		"orama-global-tor-dirauth-mon.service": install.RenderGlobalTorDirauthMonitorUnit(),
		"orama-global-tor-onion.service":       install.RenderGlobalTorOnionUnit(),
		"orama-global-tor-archive.service":     install.RenderGlobalTorArchiveUnit(true),
		"orama-global-tor-archive.timer":       install.RenderGlobalTorArchiveTimer(),
		"orama-global-tor-monitor.service":     install.RenderGlobalTorMonitorUnit(),
		"orama-global-tor-monitor.timer":       install.RenderGlobalTorMonitorTimer(),
	}
}

// oramaExecWords returns the command words of each Exec*= line of unit that runs
// the orama CLI: the words after the binary, up to the first flag or path.
func oramaExecWords(unit string) [][]string {
	var found [][]string
	for _, line := range strings.Split(unit, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || !strings.HasPrefix(key, "Exec") {
			continue
		}
		fields := strings.Fields(strings.TrimLeft(value, "+-@!:"))
		if len(fields) == 0 || path.Base(fields[0]) != "orama" {
			continue
		}
		var words []string
		for _, f := range fields[1:] {
			if strings.HasPrefix(f, "-") || strings.ContainsAny(f, "/%$=") {
				break
			}
			words = append(words, f)
		}
		found = append(found, words)
	}
	return found
}

// orama-global-chain.service ran `orama global validator check-sign-floor` after
// the command had moved under `orama maint`, and every chain on a fresh network
// failed to start (live stagenet create run, 2026-10-10). The scan of source text
// missed it: the command was written as a Go string concatenation. This reads the
// units themselves.
func TestUnitExec_everyOramaCommandAUnitRunsResolvesAndIsNodeLocal(t *testing.T) {
	units := renderedGlobalUnits()
	for _, p := range append(globOrFail(t, "../../systemd/*.service"), globOrFail(t, "../../systemd/*.timer")...) {
		body, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		units[filepath.Base(p)] = string(body)
	}
	checked := 0
	for name, unit := range units {
		for _, words := range oramaExecWords(unit) {
			root := newRootCmd()
			cmd, rest, err := root.Find(words)
			if err == nil && len(rest) > 0 && cmd.Args == nil {
				// A command that declares no arguments takes none: the words left
				// over are a subcommand it does not have.
				err = fmt.Errorf("`orama %s` has no subcommand %q", cmd.CommandPath()[len(root.Name())+1:], rest[0])
			}
			if err == nil {
				err = cmd.ValidateArgs(rest)
			}
			if err != nil || cmd == root || !cmd.Runnable() {
				t.Errorf("%s runs `orama %s`, which the CLI does not run: %v", name, strings.Join(words, " "), err)
				continue
			}
			checked++
			if !unitCommandsWithoutEnvironment[words[0]] && !cmdmeta.IsNodeLocal(cmd) {
				t.Errorf("%s runs `orama %s` with no home, but the command is not node-local", name, strings.Join(words, " "))
			}
		}
	}
	if checked == 0 {
		t.Fatal("no unit runs an orama command; the scan is broken")
	}
}

func TestOramaExecWords_readsOnlyExecLinesOfTheOramaBinary(t *testing.T) {
	unit := "[Service]\nExecStartPre=+/usr/lib/orama-global/bin/orama global validator check-sign-floor\n" +
		"ExecStart=/usr/lib/orama-global/bin/cosmovisor run start --home /x\n" +
		"ExecStart=/opt/orama/bin/orama node ipfs-gc --api 198.18.0.2:31011\n" +
		"Description=runs orama node install\n"
	got := oramaExecWords(unit)
	if len(got) != 2 || strings.Join(got[0], " ") != "global validator check-sign-floor" || strings.Join(got[1], " ") != "node ipfs-gc" {
		t.Fatalf("got %q", got)
	}
}
