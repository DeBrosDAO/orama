package checks

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

func init() {
	inspector.RegisterChecker("system", CheckSystem)
}

const systemSub = "system"

// CheckSystem runs all system-level health checks.
func CheckSystem(data *inspector.ClusterData) []inspector.CheckResult {
	var results []inspector.CheckResult

	for _, nd := range data.Nodes {
		if nd.System == nil {
			continue
		}
		results = append(results, checkSystemPerNode(nd)...)
	}

	return results
}

func checkSystemPerNode(nd *inspector.NodeData) []inspector.CheckResult {
	var r []inspector.CheckResult
	sys := nd.System
	node := nd.Node.Name()

	// 6.1 Core services active
	coreServices := []string{"debros-node", "debros-olric", "debros-ipfs", "debros-ipfs-cluster"}
	for _, svc := range coreServices {
		status, ok := sys.Services[svc]
		if !ok {
			status = "unknown"
		}
		id := fmt.Sprintf("system.svc_%s", strings.ReplaceAll(svc, "-", "_"))
		name := fmt.Sprintf("%s service active", svc)
		if status == "active" {
			r = append(r, inspector.Pass(id, name, systemSub, node, "active", inspector.Critical))
		} else {
			r = append(r, inspector.Fail(id, name, systemSub, node,
				fmt.Sprintf("status=%s", status), inspector.Critical))
		}
	}

	// 6.5 WireGuard service
	if status, ok := sys.Services["wg-quick@wg0"]; ok {
		if status == "active" {
			r = append(r, inspector.Pass("system.svc_wg", "wg-quick@wg0 active", systemSub, node, "active", inspector.Critical))
		} else {
			r = append(r, inspector.Fail("system.svc_wg", "wg-quick@wg0 active", systemSub, node,
				fmt.Sprintf("status=%s", status), inspector.Critical))
		}
	}

	// 6.3 Nameserver services (if applicable)
	if nd.Node.IsNameserver() {
		for _, svc := range []string{"coredns", "caddy"} {
			status, ok := sys.Services[svc]
			if !ok {
				status = "unknown"
			}
			id := fmt.Sprintf("system.svc_%s", svc)
			name := fmt.Sprintf("%s service active", svc)
			if status == "active" {
				r = append(r, inspector.Pass(id, name, systemSub, node, "active", inspector.Critical))
			} else {
				r = append(r, inspector.Fail(id, name, systemSub, node,
					fmt.Sprintf("status=%s", status), inspector.Critical))
			}
		}
	}

	// 6.6 Failed systemd units
	if len(sys.FailedUnits) == 0 {
		r = append(r, inspector.Pass("system.no_failed_units", "No failed systemd units", systemSub, node,
			"no failed units", inspector.High))
	} else {
		r = append(r, inspector.Fail("system.no_failed_units", "No failed systemd units", systemSub, node,
			fmt.Sprintf("failed: %s", strings.Join(sys.FailedUnits, ", ")), inspector.High))
	}

	// 6.14 Memory usage
	if sys.MemTotalMB > 0 {
		pct := float64(sys.MemUsedMB) / float64(sys.MemTotalMB) * 100
		if pct < 80 {
			r = append(r, inspector.Pass("system.memory", "Memory usage healthy", systemSub, node,
				fmt.Sprintf("used=%dMB/%dMB (%.0f%%)", sys.MemUsedMB, sys.MemTotalMB, pct), inspector.Medium))
		} else if pct < 90 {
			r = append(r, inspector.Warn("system.memory", "Memory usage healthy", systemSub, node,
				fmt.Sprintf("used=%dMB/%dMB (%.0f%%)", sys.MemUsedMB, sys.MemTotalMB, pct), inspector.High))
		} else {
			r = append(r, inspector.Fail("system.memory", "Memory usage healthy", systemSub, node,
				fmt.Sprintf("used=%dMB/%dMB (%.0f%% CRITICAL)", sys.MemUsedMB, sys.MemTotalMB, pct), inspector.Critical))
		}
	}

	// 6.15 Disk usage
	if sys.DiskUsePct > 0 {
		if sys.DiskUsePct < 80 {
			r = append(r, inspector.Pass("system.disk", "Disk usage healthy", systemSub, node,
				fmt.Sprintf("used=%s/%s (%d%%)", sys.DiskUsedGB, sys.DiskTotalGB, sys.DiskUsePct), inspector.High))
		} else if sys.DiskUsePct < 90 {
			r = append(r, inspector.Warn("system.disk", "Disk usage healthy", systemSub, node,
				fmt.Sprintf("used=%s/%s (%d%%)", sys.DiskUsedGB, sys.DiskTotalGB, sys.DiskUsePct), inspector.High))
		} else {
			r = append(r, inspector.Fail("system.disk", "Disk usage healthy", systemSub, node,
				fmt.Sprintf("used=%s/%s (%d%% CRITICAL)", sys.DiskUsedGB, sys.DiskTotalGB, sys.DiskUsePct), inspector.Critical))
		}
	}

	// 6.17 Load average vs CPU count
	if sys.LoadAvg != "" && sys.CPUCount > 0 {
		parts := strings.Split(strings.TrimSpace(sys.LoadAvg), ",")
		if len(parts) >= 1 {
			load1, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
			if err == nil {
				cpus := float64(sys.CPUCount)
				if load1 < cpus {
					r = append(r, inspector.Pass("system.load", "Load average healthy", systemSub, node,
						fmt.Sprintf("load1=%.1f cpus=%d", load1, sys.CPUCount), inspector.Medium))
				} else if load1 < cpus*2 {
					r = append(r, inspector.Warn("system.load", "Load average healthy", systemSub, node,
						fmt.Sprintf("load1=%.1f cpus=%d (elevated)", load1, sys.CPUCount), inspector.Medium))
				} else {
					r = append(r, inspector.Fail("system.load", "Load average healthy", systemSub, node,
						fmt.Sprintf("load1=%.1f cpus=%d (overloaded)", load1, sys.CPUCount), inspector.High))
				}
			}
		}
	}

	// 6.18 OOM kills
	if sys.OOMKills == 0 {
		r = append(r, inspector.Pass("system.oom", "No OOM kills", systemSub, node,
			"no OOM kills in dmesg", inspector.Critical))
	} else {
		r = append(r, inspector.Fail("system.oom", "No OOM kills", systemSub, node,
			fmt.Sprintf("%d OOM kills in dmesg", sys.OOMKills), inspector.Critical))
	}

	// 6.19 Swap usage
	if sys.SwapTotalMB > 0 {
		pct := float64(sys.SwapUsedMB) / float64(sys.SwapTotalMB) * 100
		if pct < 30 {
			r = append(r, inspector.Pass("system.swap", "Swap usage low", systemSub, node,
				fmt.Sprintf("swap=%dMB/%dMB (%.0f%%)", sys.SwapUsedMB, sys.SwapTotalMB, pct), inspector.Medium))
		} else {
			r = append(r, inspector.Warn("system.swap", "Swap usage low", systemSub, node,
				fmt.Sprintf("swap=%dMB/%dMB (%.0f%%)", sys.SwapUsedMB, sys.SwapTotalMB, pct), inspector.Medium))
		}
	}

	// 6.20 Uptime
	if sys.UptimeRaw != "" && sys.UptimeRaw != "unknown" {
		r = append(r, inspector.Pass("system.uptime", "System uptime reported", systemSub, node,
			fmt.Sprintf("up since %s", sys.UptimeRaw), inspector.Low))
	}

	// 6.21 Inode usage
	if sys.InodePct > 0 {
		if sys.InodePct < 80 {
			r = append(r, inspector.Pass("system.inodes", "Inode usage healthy", systemSub, node,
				fmt.Sprintf("inode_use=%d%%", sys.InodePct), inspector.High))
		} else if sys.InodePct < 95 {
			r = append(r, inspector.Warn("system.inodes", "Inode usage healthy", systemSub, node,
				fmt.Sprintf("inode_use=%d%% (elevated)", sys.InodePct), inspector.High))
		} else {
			r = append(r, inspector.Fail("system.inodes", "Inode usage healthy", systemSub, node,
				fmt.Sprintf("inode_use=%d%% (CRITICAL)", sys.InodePct), inspector.Critical))
		}
	}

	// 6.22 UFW firewall
	if sys.UFWActive {
		r = append(r, inspector.Pass("system.ufw", "UFW firewall active", systemSub, node,
			"ufw is active", inspector.High))
	} else {
		r = append(r, inspector.Warn("system.ufw", "UFW firewall active", systemSub, node,
			"ufw is not active", inspector.High))
	}

	// 6.23 Process user
	if sys.ProcessUser != "" && sys.ProcessUser != "unknown" {
		if sys.ProcessUser == "debros" {
			r = append(r, inspector.Pass("system.process_user", "debros-node runs as correct user", systemSub, node,
				"user=debros", inspector.High))
		} else if sys.ProcessUser == "root" {
			r = append(r, inspector.Warn("system.process_user", "debros-node runs as correct user", systemSub, node,
				"user=root (should be debros)", inspector.High))
		} else {
			r = append(r, inspector.Warn("system.process_user", "debros-node runs as correct user", systemSub, node,
				fmt.Sprintf("user=%s (expected debros)", sys.ProcessUser), inspector.Medium))
		}
	}

	// 6.24 Panic/fatal in logs
	if sys.PanicCount == 0 {
		r = append(r, inspector.Pass("system.panics", "No panics in recent logs", systemSub, node,
			"0 panic/fatal in last hour", inspector.Critical))
	} else {
		r = append(r, inspector.Fail("system.panics", "No panics in recent logs", systemSub, node,
			fmt.Sprintf("%d panic/fatal in last hour", sys.PanicCount), inspector.Critical))
	}

	// 6.25 Expected ports listening
	expectedPorts := map[int]string{
		5001: "RQLite HTTP",
		3322: "Olric Memberlist",
		6001: "Gateway",
		4501: "IPFS API",
	}
	for port, svcName := range expectedPorts {
		found := false
		for _, p := range sys.ListeningPorts {
			if p == port {
				found = true
				break
			}
		}
		if found {
			r = append(r, inspector.Pass(
				fmt.Sprintf("system.port_%d", port),
				fmt.Sprintf("%s port %d listening", svcName, port),
				systemSub, node, "port is bound", inspector.High))
		} else {
			r = append(r, inspector.Warn(
				fmt.Sprintf("system.port_%d", port),
				fmt.Sprintf("%s port %d listening", svcName, port),
				systemSub, node, "port is NOT bound", inspector.High))
		}
	}

	return r
}
