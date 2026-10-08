package report

import "github.com/DeBrosOfficial/network/pkg/updatenotice"

// collectUpdate is the auto-update agent's last finding, nil when it has none
// to report. A notice that cannot be read is a finding too: it is reported as
// refused, so a corrupt file does not hide that the agent is not working.
func collectUpdate() *updatenotice.Notice {
	n, err := updatenotice.Read(updatenotice.Path)
	if err != nil {
		return &updatenotice.Notice{State: updatenotice.StateRefused, Reason: err.Error()}
	}
	return n
}
