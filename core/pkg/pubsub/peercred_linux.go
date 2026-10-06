package pubsub

import (
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

// peerUID is the uid of the process on the other end of uc, from the kernel
// (SO_PEERCRED), as it was when that process connected.
func peerUID(uc *net.UnixConn) (uint32, error) {
	raw, err := uc.SyscallConn()
	if err != nil {
		return 0, err
	}
	var cred *unix.Ucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return 0, err
	}
	if credErr != nil {
		return 0, fmt.Errorf("SO_PEERCRED: %w", credErr)
	}
	return cred.Uid, nil
}
