//go:build e2e_fleet

package externalvantage

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/edge"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

const (
	// latencySamples per node and the loose ceiling a request from another
	// Hetzner location must stay under (a baseline, recorded in evidence).
	latencySamples = 5
	latencyCeiling = 2 * time.Second
	// curlCertError is curl's exit code for an unverifiable peer certificate.
	curlCertError = 60
)

// TestVantage_delegationChainResolves: from outside, the base name resolves
// by following the real delegation from the root — the parent zone's NS for
// the base, then the cluster's own nameservers — to the nameservers'
// addresses, and a public resolver agrees (docs/NAMESERVER_SETUP.md
// "Verification").
func TestVantage_delegationChainResolves(t *testing.T) {
	t.Parallel()
	f, ps := probes(t)
	base := f.State.BaseDomain
	want := publicIPs(edge.Nameservers(f))
	for _, p := range ps {
		requireTool(t, f, p, "dig")
		trace := f.MustExec(t, p, "dig +trace +nodnssec "+fleet.ShellQuote(base)+" A").Stdout
		if !strings.Contains(trace, "ns1."+base+".") {
			t.Errorf("%s: the trace never reaches the cluster's nameservers:\n%s", p.Name, trace)
		}
		if got := aRecords(trace, base); !slices.Equal(got, want) {
			t.Errorf("%s: the trace ends in %v, want the nameservers %v", p.Name, got, want)
		}
		public := f.MustExec(t, p, "dig +short @1.1.1.1 "+fleet.ShellQuote(base)+" A").Stdout
		if got := sortedFields(public); !slices.Equal(got, want) {
			t.Errorf("%s: 1.1.1.1 resolves %v, want %v", p.Name, got, want)
		}
	}
}

// aRecords are the addresses of name's A records in dig output.
func aRecords(out, name string) []string {
	var ips []string
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 5 && f[0] == name+"." && f[3] == "A" {
			ips = append(ips, f[4])
		}
	}
	sort.Strings(ips)
	return slices.Compact(ips)
}

func sortedFields(s string) []string {
	out := strings.Fields(s)
	sort.Strings(out)
	return out
}

func publicIPs(nodes []fleet.Node) []string {
	var out []string
	for _, n := range nodes {
		out = append(out, n.PublicIP)
	}
	sort.Strings(out)
	return out
}

// TestVantage_tlsAndHTTP11WithPinnedRoots: from outside, every node serves
// the base name over TLS that verifies against the pinned staging roots and
// only them, speaking HTTP/1.1 (docs/ARCHITECTURE.md "TLS/HTTPS").
func TestVantage_tlsAndHTTP11WithPinnedRoots(t *testing.T) {
	t.Parallel()
	f, ps := probes(t)
	base := f.State.BaseDomain
	for _, p := range ps {
		requireTool(t, f, p, "curl")
		ca := caOnProbe(t, f, p)
		for _, n := range f.State.Nodes {
			resolve := fmt.Sprintf("--resolve %s:443:%s", base, n.PublicIP)
			out := f.Exec(t, p, fmt.Sprintf("curl -sS --max-time 20 %s --cacert %s -o /dev/null -w '%%{http_version} %%{http_code}' https://%s/health", resolve, ca, base))
			if strings.TrimSpace(out.Stdout) != "1.1 200" {
				t.Errorf("%s -> %s: %q (exit %d %s), want HTTP/1.1 200 over pinned TLS", p.Name, n.Name, out.Stdout, out.Exit, out.Stderr)
			}
			sys := f.Exec(t, p, fmt.Sprintf("curl -sS --max-time 20 %s -o /dev/null https://%s/health", resolve, base))
			if sys.Exit != curlCertError {
				t.Errorf("%s -> %s with the system roots: exit %d, want %d (staging is trusted by no system store)", p.Name, n.Name, sys.Exit, curlCertError)
			}
		}
	}
}

// TestVantage_latencyBaseline: connect, TLS and total times from another
// location to each node, recorded as evidence, under a loose ceiling.
func TestVantage_latencyBaseline(t *testing.T) {
	t.Parallel()
	f, ps := probes(t)
	base := f.State.BaseDomain
	for _, p := range ps {
		ca := caOnProbe(t, f, p)
		for _, n := range f.State.Nodes {
			cmd := fmt.Sprintf("for i in $(seq %d); do curl -sS --max-time 20 --resolve %s:443:%s --cacert %s -o /dev/null -w '%%{time_connect} %%{time_appconnect} %%{time_total}\\n' https://%s/health; done",
				latencySamples, base, n.PublicIP, ca, base)
			out := f.MustExec(t, p, cmd).Stdout
			for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
				fields := strings.Fields(line)
				if len(fields) != 3 {
					t.Fatalf("%s -> %s: unreadable timing %q", p.Name, n.Name, line)
				}
				total, err := strconv.ParseFloat(fields[2], 64)
				if err != nil || time.Duration(total*float64(time.Second)) > latencyCeiling {
					t.Errorf("%s (%s) -> %s: total %ss, want under %s", p.Name, p.Location, n.Name, fields[2], latencyCeiling)
				}
			}
			t.Logf("%s (%s) -> %s (%s): connect/tls/total %s", p.Name, p.Location, n.Name, n.Location, strings.ReplaceAll(strings.TrimSpace(out), "\n", " | "))
		}
	}
}
