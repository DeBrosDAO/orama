package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/DeBrosOfficial/network/pkg/privhelper"
	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
	"github.com/DeBrosOfficial/network/pkg/version"
)

// errNodeReportRunning is the answer while another helper instance collects.
var errNodeReportRunning = errors.New("a node report collection is already running")

// nodeReportLockPath serialises collections across helper instances: the
// gateway asks every 10s, and a hung collection must not let instances pile
// up in the socket's connection slots that WireGuard and systemctl actions
// need. nodeReportTimeout bounds one collection, below the gateway's 60s
// client timeout (telemetryCollectTimeout) so the gateway reads the failure
// rather than its own timeout. Variables so tests can shorten them.
var (
	nodeReportLockPath = "/run/orama-privhelper-node-report.lock"
	nodeReportTimeout  = 50 * time.Second
)

// collectNodeReport runs every node-report collector in this (root) process.
// Tests replace the collector passed to nodeReport, not this.
func collectNodeReport() *report.NodeReport {
	return report.Collect(version.Current)
}

// nodeReport collects a report under the node-report lock and within
// nodeReportTimeout, and encodes it for the response.
//
// On a timeout the collection goroutine keeps running, and keeps the lock,
// until it ends or this per-connection process exits after responding; the
// kernel drops the lock with the process either way.
func nodeReport(collect func() *report.NodeReport) privhelper.Response {
	lock, err := lockNodeReport(nodeReportLockPath)
	if err != nil {
		return failure(err)
	}
	done := make(chan *report.NodeReport, 1)
	go func() {
		defer lock.Close() // closing the descriptor releases the flock
		done <- collect()
	}()
	timer := time.NewTimer(nodeReportTimeout)
	defer timer.Stop()
	select {
	case rpt := <-done:
		return encodeNodeReport(rpt)
	case <-timer.C:
		return failure(fmt.Errorf("the node report collection did not finish within %s", nodeReportTimeout))
	}
}

// lockNodeReport takes the node-report lock without waiting. A held lock is
// errNodeReportRunning; the caller closes the returned file to release it.
func lockNodeReport(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open the node report lock %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errNodeReportRunning
		}
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	return f, nil
}

// encodeNodeReport encodes a collected report as compact JSON for the response.
//
// The report travels as a string inside the JSON response, which the client
// reads up to privhelper.MaxRequestBytes. A report that would not fit is an
// error here rather than a truncated, unparseable answer there.
func encodeNodeReport(rpt *report.NodeReport) privhelper.Response {
	out, err := json.Marshal(rpt)
	if err != nil {
		return failure(fmt.Errorf("encode the node report: %w", err))
	}
	resp := privhelper.Response{Output: string(out)}
	encoded, err := json.Marshal(resp)
	if err != nil {
		return failure(fmt.Errorf("encode the node report response: %w", err))
	}
	// +1 for the newline json.Encoder writes after the response.
	if len(encoded)+1 > privhelper.MaxRequestBytes {
		return failure(fmt.Errorf("the node report is %d bytes encoded, over the %d-byte response limit",
			len(encoded)+1, privhelper.MaxRequestBytes))
	}
	return resp
}
