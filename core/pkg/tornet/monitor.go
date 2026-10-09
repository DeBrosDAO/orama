package tornet

import (
	"fmt"
	"path/filepath"
	"time"
)

const (
	// MonitorFile is the relay's status file in its DataDirectory
	// (constants.GlobalMonitorFile). The node report (pkg/telemetry/report)
	// parses it with report.ParseMonitor: this is the shape it reads.
	MonitorFile = "monitor.json"
	// monitorMode is readable by the Tor account's group, like the other global
	// status files.
	monitorMode = 0o640
)

// WriteRelayMonitor writes <home>/monitor.json for the node report: whether the
// consensus this relay holds lists it. It is run by a timer beside the relay,
// as the relay's own account, and replaces the file atomically.
//
// in_consensus is written only when the answer is known: the relay has an
// identity and holds a consensus that is still valid. Before the relay's first
// consensus, or once the one it holds has expired, the file is "{}" and the
// report shows the relay's state as unknown instead of a stale yes or no. The
// result is nil in that case.
func WriteRelayMonitor(home string, now time.Time) (*bool, error) {
	info, err := ReadNodeInfo(home, now)
	if err != nil {
		return nil, fmt.Errorf("read the relay's Tor state in %s: %w", home, err)
	}
	var listed *bool
	if info.Fingerprint != "" && info.Consensus != nil && info.Consensus.Valid {
		v := info.Consensus.Listed
		listed = &v
	}
	body := []byte("{}\n")
	if listed != nil {
		body = []byte(fmt.Sprintf("{\"in_consensus\":%t}\n", *listed))
	}
	if err := writeAtomic(filepath.Join(home, MonitorFile), body, monitorMode); err != nil {
		return nil, err
	}
	return listed, nil
}
