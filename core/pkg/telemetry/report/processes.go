package report

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"time"
)

// oramaProcessNames lists command substrings that identify orama-related processes.
var oramaProcessNames = []string{
	"orama", "rqlite", "olric", "ipfs", "caddy", "coredns",
}

const (
	// systemSlice is the top-level cgroup systemd puts system services in.
	systemSlice = "system.slice"
	// serviceUnitSuffix ends the cgroup of a service unit.
	serviceUnitSuffix = ".service"
	// systemdCgroupController is the controller field of the systemd
	// hierarchy on a cgroup v1 host; on cgroup v2 the one line has none.
	systemdCgroupController = "name=systemd"
)

// collectProcesses gathers zombie/orphan process info and panic counts from logs.
func collectProcesses() *ProcessReport {
	r := &ProcessReport{}

	// Run ps once and reuse the output for both zombies and orphans.
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	out, err := runCmd(ctx, "ps", "-eo", "pid,ppid,state,comm", "--no-headers")
	if err == nil {
		r.Zombies, r.Orphans = classifyProcesses(out, readProcCgroup)
	}

	r.ZombieCount = len(r.Zombies)
	r.OrphanCount = len(r.Orphans)

	// PanicCount: check journal for panic/fatal in last hour.
	{
		ctx2, cancel2 := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel2()

		out, err := runCmd(ctx2, "bash", "-c",
			`journalctl -u orama-node --no-pager -n 500 --since "1 hour ago" 2>/dev/null | grep -ciE "(panic|fatal)" || echo 0`)
		if err == nil {
			if n, err := strconv.Atoi(strings.TrimSpace(out)); err == nil {
				r.PanicCount = n
			}
		}
	}

	return r
}

// classifyProcesses reads `ps -eo pid,ppid,state,comm` output. A zombie is
// state Z. An orphan is an orama-related process whose parent is init and
// that no system service unit owns: a service's own processes are reparented
// to init too, so the parent says nothing about who supervises the process,
// and its cgroup does. cgroupOf returns a process's /proc/<pid>/cgroup.
func classifyProcesses(psOut string, cgroupOf func(pid int) (string, error)) (zombies, orphans []ProcessInfo) {
	for _, line := range strings.Split(psOut, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		pid, _ := strconv.Atoi(fields[0])
		ppid, _ := strconv.Atoi(fields[1])
		proc := ProcessInfo{PID: pid, PPID: ppid, State: fields[2], Command: strings.Join(fields[3:], " ")}

		if proc.State == "Z" {
			zombies = append(zombies, proc)
		}
		if ppid != 1 || !isOramaProcess(proc.Command) {
			continue
		}
		cgroup, err := cgroupOf(pid)
		if errors.Is(err, fs.ErrNotExist) {
			continue // exited since ps ran
		}
		// A cgroup that cannot be read proves no owner: the process counts.
		if err != nil || owningServiceUnit(cgroup) == "" {
			orphans = append(orphans, proc)
		}
	}
	return zombies, orphans
}

// readProcCgroup is the content of /proc/<pid>/cgroup.
func readProcCgroup(pid int) (string, error) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cgroup")
	return string(data), err
}

// owningServiceUnit returns the system service unit whose cgroup holds the
// process, from the content of its /proc/<pid>/cgroup, or "" when it is in
// none: a session scope, a user slice, a scope started by hand. Units are
// found by cgroup, not by a list of names, so a service installed later (the
// global chain, indexer, reporter, a namespace's) is owned the day it exists.
func owningServiceUnit(cgroup string) string {
	for _, line := range strings.Split(cgroup, "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), ":", 3)
		if len(parts) != 3 || (parts[1] != "" && parts[1] != systemdCgroupController) {
			continue
		}
		dirs := strings.Split(strings.Trim(parts[2], "/"), "/")
		unit := dirs[len(dirs)-1]
		if dirs[0] == systemSlice && strings.HasSuffix(unit, serviceUnitSuffix) {
			return unit
		}
	}
	return ""
}

// isOramaProcess checks if a command string contains any orama-related process name.
func isOramaProcess(command string) bool {
	lower := strings.ToLower(command)
	for _, name := range oramaProcessNames {
		if strings.Contains(lower, name) {
			return true
		}
	}
	return false
}
