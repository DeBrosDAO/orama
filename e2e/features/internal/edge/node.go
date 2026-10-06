//go:build e2e_fleet

package edge

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// statusMarker separates a probe's body from the status curl prints after it.
const statusMarker = "\n__E2E_HTTP_STATUS__="

// Probe is one HTTP answer seen from a node shell.
type Probe struct {
	Status int
	Body   string
	Exit   int
	Stderr string
}

// NodeCurl is a curl invocation run on a node: Method, URL, Headers ("K: V",
// each passed verbatim) and Body (sent verbatim from a quoted argument, never
// through a file the test leaves behind). Prefix runs before curl in the same
// shell (to feed curl's stdin, for instance).
type NodeCurl struct {
	Method  string
	URL     string
	Headers []string
	Body    string
	Prefix  string
}

// Command renders the shell command.
func (c NodeCurl) Command() string {
	method := c.Method
	if method == "" {
		method = "GET"
	}
	var b strings.Builder
	b.WriteString(c.Prefix)
	b.WriteString("curl -sS --max-time 20 -X " + method)
	for _, h := range c.Headers {
		// Headers are single-quoted like every other argument: a header
		// value holding a quote, a $ or a backtick (a forged JSON claim) is
		// sent verbatim, never read by the node's shell.
		b.WriteString(" -H " + fleet.ShellQuote(h))
	}
	if c.Body != "" {
		b.WriteString(" --data-binary " + fleet.ShellQuote(c.Body))
	}
	b.WriteString(" -w '" + strings.ReplaceAll(statusMarker, "\n", `\n`) + "%{http_code}' ")
	b.WriteString(fleet.ShellQuote(c.URL))
	return b.String()
}

// Run runs c on n.
func (c NodeCurl) Run(t testing.TB, f *fleet.Fleet, n fleet.Node) Probe {
	t.Helper()
	return ParseProbe(f.Exec(t, n, c.Command()))
}

// ParseProbe splits curl's output into body and status.
func ParseProbe(out fleet.Output) Probe {
	p := Probe{Exit: out.Exit, Body: out.Stdout, Stderr: out.Stderr}
	i := strings.LastIndex(out.Stdout, statusMarker)
	if i < 0 {
		return p
	}
	p.Body = out.Stdout[:i]
	p.Status, _ = strconv.Atoi(strings.TrimSpace(out.Stdout[i+len(statusMarker):]))
	return p
}

// LocalGateway is the index gateway's URL on the node's loopback.
func LocalGateway(path string) string {
	return fmt.Sprintf("http://127.0.0.1:%d%s", GatewayPort, path)
}

// OverlayGateway is n's index gateway on its WireGuard address.
func OverlayGateway(n fleet.Node, path string) string {
	return fmt.Sprintf("http://%s:%d%s", n.WGIP, GatewayPort, path)
}

// MainPID is the unit's main process id on n, or fails the test.
func MainPID(t testing.TB, f *fleet.Fleet, n fleet.Node, unit string) int {
	t.Helper()
	out := f.MustExec(t, n, "systemctl show -p MainPID --value "+fleet.ShellQuote(unit))
	pid, err := strconv.Atoi(strings.TrimSpace(out.Stdout))
	if err != nil || pid <= 0 {
		t.Fatalf("%s: %s has no main process (MainPID=%q): is it running?", n.Name, unit, out.Stdout)
	}
	return pid
}

// ProcessUser is the user name the process pid runs as on n. `ps -o user=`
// cuts a name at eight characters ("orama-c+"), so the column is widened.
func ProcessUser(t testing.TB, f *fleet.Fleet, n fleet.Node, pid int) string {
	t.Helper()
	out := f.MustExec(t, n, fmt.Sprintf("ps -o user:32= -p %d", pid))
	return strings.TrimSpace(out.Stdout)
}

// UnitProperty reads one systemd property of unit on n.
func UnitProperty(t testing.TB, f *fleet.Fleet, n fleet.Node, unit, prop string) string {
	t.Helper()
	out := f.MustExec(t, n, "systemctl show -p "+prop+" --value "+fleet.ShellQuote(unit))
	return strings.TrimSpace(out.Stdout)
}

// Nameservers are the run's nodes that answer DNS: those installed with the
// nameserver role, or every core node when the run marks none.
func Nameservers(f *fleet.Fleet) []fleet.Node {
	var out []fleet.Node
	for _, n := range f.State.Nodes {
		if n.Role == fleet.RoleNameserver {
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		return f.State.Nodes
	}
	return out
}

// Workers are the core nodes without the nameserver role.
func Workers(f *fleet.Fleet) []fleet.Node {
	var out []fleet.Node
	for _, n := range f.State.Nodes {
		if n.Role != fleet.RoleNameserver {
			out = append(out, n)
		}
	}
	return out
}
