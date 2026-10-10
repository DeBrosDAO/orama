package artifacts

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
	"github.com/DeBrosOfficial/network/e2e/harness/secrets"
)

// ItemBudget bounds one collected command.
const ItemBudget = 2 * time.Minute

// JournalLines bounds one unit's journal.
const JournalLines = 5000

// UnitPatterns are the units whose journals are collected on every node.
var UnitPatterns = []string{"orama-*", "caddy*", "coredns*", "wg-quick@*"}

// nodeCommands are the state snapshots taken on every node. They are chosen
// to carry no private key: `wg show all` hides the interface key, unlike
// `wg show all dump`.
var nodeCommands = []struct{ name, cmd string }{
	{"node-report.json", "orama node report --json"},
	{"failed-units.txt", "systemctl --failed --no-legend --plain"},
	{"listeners.txt", "ss -H -ltnup"},
	{"wireguard.txt", "wg show all"},
	{"firewall.txt", "ufw status verbose"},
	{"disk.txt", "df -h"},
	{"clock.txt", "timedatectl"},
}

// chainCommand is collected only when the run has a chain: on a run without
// one it would record a failed item for something that was never there.
var chainCommand = struct{ name, cmd string }{"chain-status.json", "curl -s --max-time 5 http://127.0.0.1:26657/status"}

var unitName = regexp.MustCompile(`^[A-Za-z0-9@._:-]+$`)

// nodeName is what a node name may be to become a directory of the
// collection: node-1, extra-join, probe-2. Anything else ("../x", "a/b") is
// refused rather than written outside the collection dir.
var nodeName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)

// FleetCollector collects from every node over SSH and from the runner's CLI.
type FleetCollector struct {
	Fleet *fleet.Fleet
	// CLI is the run's CLI; nil skips the CLI-side reports.
	CLI *oramacli.Runner
	// Since bounds the journals (the run's start).
	Since    time.Time
	MaxBytes int
	Redactor *secrets.Redactor
}

// Collect gathers everything into dir. An item that cannot be collected is
// recorded in the index with its error; only failing to write locally is an
// error, because the collection runs precisely when things are broken.
func (c *FleetCollector) Collect(ctx context.Context, dir string) (Index, error) {
	red := c.Redactor
	if red == nil {
		red = secrets.NewRedactor()
	}
	w := &writer{dir: dir, maxBytes: c.MaxBytes, red: red}
	if w.maxBytes <= 0 {
		w.maxBytes = DefaultMaxBytes
	}
	nodes, errs := c.readableNodes(ctx, w, red)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, n := range nodes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, it := range c.nodeItems(ctx, n) {
				mu.Lock()
				errs = append(errs, w.write(it.rel, n.Name, it.cmd, it.out, it.err))
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	for _, it := range c.cliItems(ctx) {
		errs = append(errs, w.write(it.rel, "cli", it.cmd, it.out, it.err))
	}
	idx, err := w.finish()
	return idx, errors.Join(append(errs, err)...)
}

// readableNodes are the nodes to collect from: every node with a valid
// name whose secrets were read and registered with red first (a secret of
// one node can show up in another's journal, so all are read before any
// is collected). A node whose secrets cannot be read is not collected from
// at all: its output could not be redacted of them.
func (c *FleetCollector) readableNodes(ctx context.Context, w *writer, red *secrets.Redactor) ([]fleet.Node, []error) {
	var nodes []fleet.Node
	var errs []error
	for _, n := range c.Fleet.AllNodes() {
		if !nodeName.MatchString(n.Name) {
			errs = append(errs, fmt.Errorf("node %q: not a node name, nothing collected from %s", n.Name, n.PublicIP))
			continue
		}
		if err := registerNodeSecrets(ctx, c.Fleet.SSH(ctx, n), red); err != nil {
			errs = append(errs, w.write(path.Join("nodes", n.Name, withheldName), n.Name, "(node secrets)", secrets.Withheld, err))
			continue
		}
		nodes = append(nodes, n)
	}
	return nodes, errs
}

// withheldName records a node nothing was collected from, and why.
const withheldName = "withheld.txt"

type item struct {
	rel, cmd, out string
	err           error
}

func (c *FleetCollector) nodeItems(ctx context.Context, n fleet.Node) []item {
	sh := c.Fleet.SSH(ctx, n)
	base := path.Join("nodes", n.Name)
	cmds := nodeCommands
	if c.Fleet.State.ChainID != "" {
		cmds = append(append(cmds[:0:0], nodeCommands...), chainCommand)
	}
	var out []item
	for _, nc := range cmds {
		out = append(out, runItem(ctx, sh, path.Join(base, nc.name), nc.cmd))
	}
	listCmd := "systemctl list-units --all --no-legend --plain " + strings.Join(quoteAll(UnitPatterns), " ")
	units := runItem(ctx, sh, path.Join(base, "units.txt"), listCmd)
	out = append(out, units)
	for _, u := range ParseUnits(units.out) {
		cmd := fmt.Sprintf("journalctl -u %s --since @%d --no-pager -o short-iso -n %d", u, c.Since.Unix(), JournalLines)
		out = append(out, runItem(ctx, sh, path.Join(base, "journal", u+".log"), cmd))
	}
	return out
}

func runItem(ctx context.Context, sh fleet.Shell, rel, cmd string) item {
	ctx, cancel := context.WithTimeout(ctx, ItemBudget)
	defer cancel()
	o, err := sh.Run(ctx, cmd)
	it := item{rel: rel, cmd: cmd, out: o.Stdout}
	if o.Stderr != "" {
		it.out += "\n[stderr]\n" + o.Stderr
	}
	switch {
	case err != nil:
		it.err = err
	case o.Exit != 0:
		it.err = fmt.Errorf("exit %d", o.Exit)
	}
	return it
}

// ParseUnits reads unit names from `systemctl list-units --plain --no-legend`.
func ParseUnits(out string) []string {
	var units []string
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || !unitName.MatchString(fields[0]) {
			continue
		}
		units = append(units, fields[0])
	}
	return units
}

func quoteAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = fleet.ShellQuote(s)
	}
	return out
}

func (c *FleetCollector) cliItems(ctx context.Context) []item {
	if c.CLI == nil {
		return nil
	}
	env := c.Fleet.State.Env
	cmds := []struct {
		rel  string
		args []string
	}{
		{"cli/monitor-report.json", []string{"status", "report", "--json", "--env", env}},
		{"cli/inspect.json", []string{"inspect", "--env", env, "--format", "json"}},
	}
	var out []item
	for _, cc := range cmds {
		ictx, cancel := context.WithTimeout(ctx, ItemBudget)
		res, err := c.CLI.Run(ictx, cc.args...)
		cancel()
		it := item{rel: cc.rel, cmd: "orama " + strings.Join(cc.args, " "), out: res.Stdout}
		if res.Stderr != "" {
			it.out += "\n[stderr]\n" + res.Stderr
		}
		if err == nil && res.Exit != 0 {
			err = errors.New("exit " + strconv.Itoa(res.Exit))
		}
		it.err = err
		out = append(out, it)
	}
	return out
}
