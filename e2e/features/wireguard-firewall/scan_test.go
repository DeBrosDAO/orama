//go:build e2e_fleet

package wireguardfirewall

import (
	"context"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

const (
	// probeTimeout bounds one TCP connect from the runner.
	probeTimeout = 3 * time.Second
	// scanWorkers bounds concurrent connects per node.
	scanWorkers = 64
)

// scanPorts are the TCP ports probed from outside: the well-known range, the
// tenant and index blocks (10000-10199), the chain and global blocks
// (31000-31025), and the old ports previous releases listened on.
func scanPorts() []int {
	var ports []int
	for p := 1; p <= 1024; p++ {
		ports = append(ports, p)
	}
	for p := 10000; p <= 10199; p++ {
		ports = append(ports, p)
	}
	for p := infra.ChainP2PPort; p <= infra.ChainP2PPort+25; p++ {
		ports = append(ports, p)
	}
	return append(ports, 2019, 3320, 3322, 4001, 5001, 7001, 8080, 9050, 9051, 9094, 9096, 9998, 9999)
}

// openFromOutside returns the ports of ip that accept a TCP connection from
// the runner.
func openFromOutside(ctx context.Context, ip string, ports []int) []int {
	var (
		mu   sync.Mutex
		open []int
		wg   sync.WaitGroup
	)
	jobs := make(chan int)
	for range scanWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d := net.Dialer{Timeout: probeTimeout}
			for p := range jobs {
				c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip, strconv.Itoa(p)))
				if err != nil {
					continue
				}
				c.Close()
				mu.Lock()
				open = append(open, p)
				mu.Unlock()
			}
		}()
	}
	for _, p := range ports {
		jobs <- p
	}
	close(jobs)
	wg.Wait()
	return open
}

// TestScan_onlyEdgePortsOpen: from the runner, over the internet, each
// node's public address accepts TCP on SSH, HTTP and HTTPS, on 53 only when
// it is a nameserver, on the TURN ports only while it relays, and on nothing
// else scanned (docs/SECURITY.md "Network Isolation"). The run's cloud
// firewall also filters, so only ports it lets through can reveal a leak.
func TestScan_onlyEdgePortsOpen(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	ports := scanPorts()
	for _, n := range f.State.Nodes {
		turn := infra.HostRunsTURN(t, f, n)
		open := openFromOutside(t.Context(), n.PublicIP, ports)
		got := map[int]bool{}
		for _, p := range open {
			got[p] = true
			if !allowedPublic(fleet.Listener{Proto: "tcp", Port: p}, n, turn) {
				t.Errorf("%s: tcp %d is open from the internet", n.Name, p)
			}
		}
		for _, p := range []int{80, 443} {
			if !got[p] {
				t.Errorf("%s: tcp %d is not reachable from the internet", n.Name, p)
			}
		}
		if n.Role == fleet.RoleNameserver && !got[nameserverPort] {
			t.Errorf("nameserver %s: tcp 53 is not reachable", n.Name)
		}
	}
}
