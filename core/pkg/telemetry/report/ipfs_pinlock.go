package report

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

// Kubo serialises pinning and garbage collection with one lock: pin/add and
// pin/update hold it while they fetch the whole DAG, and repo/gc takes it
// exclusively. A pin/add whose content no peer has holds it until something
// cancels the request, and repo gc — the orama-namespace-ipfs-gc oneshot —
// waits behind it until its unit times out, so no space is reclaimed. Kubo's
// diag/cmds lists the active requests; the report keeps the oldest one of
// these three, which is the one everything else is queued behind.

// pinLockCommands are the Kubo commands that hold or wait for the pin lock.
var pinLockCommands = map[string]bool{
	"pin/add":    true,
	"pin/update": true,
	"repo/gc":    true,
}

// kuboActiveRequest is the part of a diag/cmds entry the report reads. The
// entry's Options are not decoded: a CLI request's options carry its
// --api-auth bearer.
type kuboActiveRequest struct {
	Command   string    `json:"Command"`
	Active    bool      `json:"Active"`
	StartTime time.Time `json:"StartTime"`
}

// collectIPFSPinLock records the oldest active pin-lock command on r.
func collectIPFSPinLock(r *IPFSReport) {
	body, err := ipfsPost(constants.LocalIPFSAPIURL() + "/api/v0/diag/cmds")
	if err != nil {
		r.PinLockError = fmt.Sprintf("diag/cmds: %v", err)
		return
	}
	cmd, age, err := oldestPinLockCommand(body, time.Now())
	if err != nil {
		r.PinLockError = err.Error()
		return
	}
	r.OldestPinLockCmd = cmd
	r.OldestPinLockAgeSeconds = int64(age / time.Second)
}

// oldestPinLockCommand returns the active pin-lock command that started
// first and how long it has run, or "" when none is active.
func oldestPinLockCommand(body []byte, now time.Time) (string, time.Duration, error) {
	var reqs []kuboActiveRequest
	if err := json.Unmarshal(body, &reqs); err != nil {
		return "", 0, fmt.Errorf("decode diag/cmds: %w", err)
	}
	var oldest *kuboActiveRequest
	for i := range reqs {
		req := &reqs[i]
		if !req.Active || !pinLockCommands[req.Command] || req.StartTime.IsZero() {
			continue
		}
		if oldest == nil || req.StartTime.Before(oldest.StartTime) {
			oldest = req
		}
	}
	if oldest == nil {
		return "", 0, nil
	}
	age := now.Sub(oldest.StartTime)
	if age < 0 {
		age = 0
	}
	return oldest.Command, age, nil
}
