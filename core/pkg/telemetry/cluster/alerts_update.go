package cluster

import (
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
	"github.com/DeBrosOfficial/network/pkg/updatenotice"
)

// checkNodeUpdate turns the node's auto-update finding into an alert. A newer
// release that was not installed is information; one that was refused or that
// this node rolled back is a warning, since a refusal can be a repository that
// has frozen or been tampered with.
func checkNodeUpdate(r *report.NodeReport, host string) []Alert {
	n := r.Update
	if n == nil {
		return nil
	}
	switch n.State {
	case updatenotice.StateAvailable:
		return []Alert{{AlertInfo, "update", host, fmt.Sprintf(
			"Release %s is available on the %s channel (running %s, auto-update is %s)", n.Candidate, n.Channel, n.Current, n.Mode)}}
	case updatenotice.StateRefused:
		return []Alert{{AlertWarning, "update", host, fmt.Sprintf(
			"Release %s was refused on the %s channel: %s", orUnknown(n.Candidate), n.Channel, n.Reason)}}
	case updatenotice.StateFailed:
		return []Alert{{AlertWarning, "update", host, fmt.Sprintf(
			"Installing release %s failed and was rolled back (running %s): %s", n.Candidate, n.Current, n.Reason)}}
	}
	return nil
}

func orUnknown(version string) string {
	if version == "" {
		return "(unknown)"
	}
	return version
}
