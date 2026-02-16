package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/cli/monitor"
)

// renderServicesTab renders a cross-node service matrix.
func renderServicesTab(snap *monitor.ClusterSnapshot, width int) string {
	if snap == nil {
		return styleMuted.Render("Collecting cluster data...")
	}

	reports := snap.Healthy()
	if len(reports) == 0 {
		return styleMuted.Render("No healthy nodes to display.")
	}

	var b strings.Builder

	// Collect all unique service names across nodes
	svcSet := make(map[string]bool)
	for _, r := range reports {
		if r.Services == nil {
			continue
		}
		for _, svc := range r.Services.Services {
			svcSet[svc.Name] = true
		}
	}

	svcNames := make([]string, 0, len(svcSet))
	for name := range svcSet {
		svcNames = append(svcNames, name)
	}
	sort.Strings(svcNames)

	if len(svcNames) == 0 {
		return styleMuted.Render("No services found on any node.")
	}

	b.WriteString(styleBold.Render("Service Matrix"))
	b.WriteString("\n")
	b.WriteString(separator(width))
	b.WriteString("\n\n")

	// Header: service name + each node host
	header := fmt.Sprintf("  %-28s", headerStyle.Render("SERVICE"))
	for _, r := range reports {
		host := nodeHost(r)
		if len(host) > 15 {
			host = host[:15]
		}
		header += fmt.Sprintf(" %-17s", headerStyle.Render(host))
	}
	b.WriteString(header)
	b.WriteString("\n")

	// Build a lookup: host -> service name -> ServiceInfo
	type svcKey struct {
		host string
		name string
	}
	svcMap := make(map[svcKey]string) // status string
	for _, r := range reports {
		host := nodeHost(r)
		if r.Services == nil {
			continue
		}
		for _, svc := range r.Services.Services {
			var st string
			switch {
			case svc.ActiveState == "active":
				st = styleHealthy.Render("active")
			case svc.ActiveState == "failed":
				st = styleCritical.Render("FAILED")
			case svc.ActiveState == "":
				st = styleMuted.Render("n/a")
			default:
				st = styleWarning.Render(svc.ActiveState)
			}
			if svc.RestartLoopRisk {
				st = styleCritical.Render("LOOP!")
			}
			svcMap[svcKey{host, svc.Name}] = st
		}
	}

	// Rows
	for _, svcName := range svcNames {
		row := fmt.Sprintf("  %-28s", svcName)
		for _, r := range reports {
			host := nodeHost(r)
			st, ok := svcMap[svcKey{host, svcName}]
			if !ok {
				st = styleMuted.Render("-")
			}
			row += fmt.Sprintf(" %-17s", st)
		}
		b.WriteString(row)
		b.WriteString("\n")
	}

	// Failed units per node
	hasFailedUnits := false
	for _, r := range reports {
		if r.Services != nil && len(r.Services.FailedUnits) > 0 {
			hasFailedUnits = true
			break
		}
	}
	if hasFailedUnits {
		b.WriteString("\n")
		b.WriteString(styleBold.Render("Failed Systemd Units"))
		b.WriteString("\n")
		b.WriteString(separator(width))
		b.WriteString("\n")
		for _, r := range reports {
			if r.Services == nil || len(r.Services.FailedUnits) == 0 {
				continue
			}
			b.WriteString(fmt.Sprintf("  %s: %s\n",
				styleBold.Render(nodeHost(r)),
				styleCritical.Render(strings.Join(r.Services.FailedUnits, ", ")),
			))
		}
	}

	return b.String()
}
