//go:build linux

package ipfs

import (
	"fmt"
	"net/netip"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"
)

// sockDiagTimeout bounds the kernel's answer; it is local and immediate.
const sockDiagTimeout = 2 * time.Second

// sockDiagReplyBuf fits one inet_diag_msg with its attributes.
const sockDiagReplyBuf = 8192

var sockDiagSeq atomic.Uint32

// sockDiagOwner asks the kernel which uid owns the IPv4 TCP socket bound to
// local and connected to remote.
func sockDiagOwner(local, remote netip.AddrPort) (uint32, error) {
	req, err := sockDiagRequest(local, remote, sockDiagSeq.Add(1))
	if err != nil {
		return 0, err
	}
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_SOCK_DIAG)
	if err != nil {
		return 0, fmt.Errorf("open a sock_diag socket: %w", err)
	}
	defer unix.Close(fd)
	tv := unix.NsecToTimeval(sockDiagTimeout.Nanoseconds())
	if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &tv); err != nil {
		return 0, fmt.Errorf("bound the sock_diag answer: %w", err)
	}
	if err := unix.Sendto(fd, req, 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return 0, fmt.Errorf("ask sock_diag for %s->%s: %w", local, remote, err)
	}
	buf := make([]byte, sockDiagReplyBuf)
	n, _, err := unix.Recvfrom(fd, buf, 0)
	if err != nil {
		return 0, fmt.Errorf("read the sock_diag answer for %s->%s: %w", local, remote, err)
	}
	uid, err := parseSockDiagReply(buf[:n])
	if err != nil {
		return 0, fmt.Errorf("%s->%s: %w", local, remote, err)
	}
	return uid, nil
}
