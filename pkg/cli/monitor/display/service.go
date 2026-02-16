package display

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/cli/monitor"
)

// ServiceTable prints a cross-node service status matrix to w.
func ServiceTable(snap *monitor.ClusterSnapshot, w io.Writer) error {
	fmt.Fprintf(w, "%s\n", styleBold.Render(
		fmt.Sprintf("Service Status Matrix \u2014 %s", snap.Environment)))
	fmt.Fprintln(w, strings.Repeat("\u2550", 36))
	fmt.Fprintln(w)

	// Collect all service names and build per-host maps
	type hostServices struct {
		host     string
		shortIP  string
		services map[string]string // name -> active_state
	}

	var hosts []hostServices
	serviceSet := map[string]bool{}

	for _, cs := range snap.Nodes {
		if cs.Error != nil || cs.Report == nil || cs.Report.Services == nil {
			continue
		}
		hs := hostServices{
			host:     cs.Node.Host,
			shortIP:  shortIP(cs.Node.Host),
			services: make(map[string]string),
		}
		for _, svc := range cs.Report.Services.Services {
			hs.services[svc.Name] = svc.ActiveState
			serviceSet[svc.Name] = true
		}
		hosts = append(hosts, hs)
	}

	// Sort service names
	var svcNames []string
	for name := range serviceSet {
		svcNames = append(svcNames, name)
	}
	sort.Strings(svcNames)

	if len(hosts) == 0 || len(svcNames) == 0 {
		fmt.Fprintln(w, styleMuted.Render("  No service data available"))
		return nil
	}

	// Header: SERVICE + each host short IP
	hdr := fmt.Sprintf("%-22s", styleHeader.Render("SERVICE"))
	for _, h := range hosts {
		hdr += fmt.Sprintf("%-12s", styleHeader.Render(h.shortIP))
	}
	fmt.Fprintln(w, hdr)
	fmt.Fprintln(w, separator(22+12*len(hosts)))

	// Rows
	for _, name := range svcNames {
		row := fmt.Sprintf("%-22s", name)
		for _, h := range hosts {
			state, ok := h.services[name]
			if !ok {
				row += fmt.Sprintf("%-12s", styleMuted.Render("--"))
			} else {
				row += fmt.Sprintf("%-12s", colorServiceState(state))
			}
		}
		fmt.Fprintln(w, row)
	}

	return nil
}

// ServiceJSON writes the service matrix as JSON.
func ServiceJSON(snap *monitor.ClusterSnapshot, w io.Writer) error {
	type svcEntry struct {
		Host     string            `json:"host"`
		Services map[string]string `json:"services"`
	}

	var entries []svcEntry
	for _, cs := range snap.Nodes {
		if cs.Error != nil || cs.Report == nil || cs.Report.Services == nil {
			continue
		}
		e := svcEntry{
			Host:     cs.Node.Host,
			Services: make(map[string]string),
		}
		for _, svc := range cs.Report.Services.Services {
			e.Services[svc.Name] = svc.ActiveState
		}
		entries = append(entries, e)
	}

	return writeJSON(w, entries)
}

// shortIP truncates an IP to the first 3 octets for compact display.
func shortIP(ip string) string {
	parts := strings.Split(ip, ".")
	if len(parts) == 4 {
		return parts[0] + "." + parts[1] + "." + parts[2]
	}
	if len(ip) > 12 {
		return ip[:12]
	}
	return ip
}

// colorServiceState renders a service state with appropriate color.
func colorServiceState(state string) string {
	switch state {
	case "active":
		return styleGreen.Render("ACTIVE")
	case "failed":
		return styleRed.Render("FAILED")
	case "inactive":
		return styleMuted.Render("inactive")
	default:
		return styleYellow.Render(state)
	}
}
