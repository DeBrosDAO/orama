package main

import (
	"fmt"
	"net"
	"os"

	"github.com/DeBrosOfficial/network/pkg/privhelper"
	"golang.org/x/sys/unix"
)

// identifyCaller asks the kernel who is on the other end of the connection:
// its uid (SO_PEERCRED) and, for anyone but root, the systemd unit its process
// runs in (identifyUnit).
func identifyCaller(uc *net.UnixConn) (privhelper.Caller, error) {
	raw, err := uc.SyscallConn()
	if err != nil {
		return privhelper.Caller{}, err
	}
	var cred *unix.Ucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return privhelper.Caller{}, err
	}
	if credErr != nil {
		return privhelper.Caller{}, fmt.Errorf("SO_PEERCRED: %w", credErr)
	}
	caller := privhelper.Caller{UID: cred.Uid}
	if cred.Uid == 0 {
		return caller, nil
	}
	caller.Unit, err = identifyUnit(int(cred.Pid))
	return caller, err
}

// identifyUnit is the unit the process pid runs in, from /proc/<pid>/cgroup.
//
// A pid is a name that can be reused, so reading /proc by it is only good if it
// still names the process that connected. Three things hold that together:
//
//  1. A pidfd is opened first. It refers to one process for as long as it is
//     open, whatever happens to the number.
//  2. The process must have started no later than this helper did. The caller
//     connected before systemd accepted the connection and started this
//     instance, so a later start time is a different process that took the
//     number over.
//  3. After /proc has been read, the pidfd's process must still be alive: then
//     the pid has not been released since the pidfd was opened, and the
//     start time and cgroup read are that process's.
//
// What remains is a caller that exits and has its number reused between
// connecting and this instance starting, by a process of the right unit — which
// needs the pid space to wrap in milliseconds.
func identifyUnit(pid int) (string, error) {
	pidfd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		return "", fmt.Errorf("open a pidfd for caller %d: %w", pid, err)
	}
	defer unix.Close(pidfd)

	callerStart, err := readStartTime(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", err
	}
	selfStart, err := readStartTime("/proc/self/stat")
	if err != nil {
		return "", err
	}
	cgroup, err := os.ReadFile(fmt.Sprintf("/proc/%d/cgroup", pid))
	if err != nil {
		return "", fmt.Errorf("read caller %d's cgroup: %w", pid, err)
	}
	if err := unix.PidfdSendSignal(pidfd, 0, nil, 0); err != nil {
		return "", fmt.Errorf("caller %d exited while it was being identified: %w", pid, err)
	}
	if callerStart > selfStart {
		return "", fmt.Errorf("process %d started after this request was accepted; its pid was reused", pid)
	}
	return privhelper.UnitFromCgroup(string(cgroup))
}

func readStartTime(path string) (uint64, error) {
	stat, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", path, err)
	}
	return parseStartTime(string(stat))
}
