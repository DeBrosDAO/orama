//go:build linux

package sandbox

import "syscall"

// isolationAttrs are the process attributes every sandboxed service starts
// with: its own mount and hostname namespaces, and the service's own uid/gid.
//
// CLONE_NEWPID is deliberately absent — it would make the service PID 1 in its
// namespace, and PID 1 ignores SIGTERM by default, which changes how every
// restart behaves.
func isolationAttrs(uid, gid uint32) *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWNS | // mount namespace
			syscall.CLONE_NEWUTS, // hostname namespace
		Credential: &syscall.Credential{Uid: uid, Gid: gid},
	}
}
