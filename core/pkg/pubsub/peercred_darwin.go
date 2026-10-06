package pubsub

import (
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

// peerUID is the uid of the process on the other end of uc (LOCAL_PEERCRED).
// Nodes run Linux; this is what lets the listener be tested on a developer's
// machine.
func peerUID(uc *net.UnixConn) (uint32, error) {
	raw, err := uc.SyscallConn()
	if err != nil {
		return 0, err
	}
	var cred *unix.Xucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	}); err != nil {
		return 0, err
	}
	if credErr != nil {
		return 0, fmt.Errorf("LOCAL_PEERCRED: %w", credErr)
	}
	return cred.Uid, nil
}
