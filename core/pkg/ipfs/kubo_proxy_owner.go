package ipfs

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
)

// The proxy adds Kubo's bearer to whatever it forwards, so who may dial it is
// the access check. Loopback TCP has no file mode, and a tenant deployment is
// allowed to reach localhost. The kernel records the uid that created every
// socket and answers for it by address (sock_diag), so the
// proxy accepts a connection only when the dialling socket belongs to its own
// uid — the orama user, which already holds the cluster secret the bearer is
// derived from. A DynamicUser deployment has another uid and is closed.

// The owner is asked of the kernel directly with a sock_diag query
// (NETLINK_SOCK_DIAG, SOCK_DIAG_BY_FAMILY) naming the exact socket: one
// lookup per connection whatever the number of sockets on the node, and one
// that cannot miss the row. Reading /proc/net/tcp instead meant parsing the
// whole table — thousands of rows beside Kubo's swarm — for every connection,
// and the kernel serves that table in chunks it resumes by position, so a row
// could be skipped while sockets came and went.

// errSocketNotFound is a connection whose dialling socket the kernel no
// longer has, e.g. because it closed before it was looked up.
var errSocketNotFound = errors.New("socket not found")

// Wire layout of the sock_diag exchange (linux/inet_diag.h, linux/netlink.h).
// Ports and addresses are in network byte order; everything else is native.
const (
	nlmsgHeaderLen     = 16
	inetDiagReqV2Len   = 56
	inetDiagMsgUIDOff  = 64
	inetDiagMsgMinLen  = 72
	sockDiagByFamily   = 20 // SOCK_DIAG_BY_FAMILY
	nlmsgError         = 2  // NLMSG_ERROR
	nlmFRequest        = 0x1
	ipprotoTCP         = 6
	afInet             = 2
	enoent             = 2
	inetDiagAllStates  = 0xffffffff
	inetDiagNoCookie   = 0xffffffff
	sockIDSportOff     = 8
	sockIDSrcOff       = 12
	sockIDDstOff       = 28
	sockIDCookieOff    = 48
	nlmsgErrorCodeSize = 4
)

// sockDiagRequest is the netlink message asking for the IPv4 TCP socket
// bound to local and connected to remote.
func sockDiagRequest(local, remote netip.AddrPort, seq uint32) ([]byte, error) {
	src, dst := local.Addr().Unmap(), remote.Addr().Unmap()
	if !src.Is4() || !dst.Is4() {
		return nil, fmt.Errorf("%s->%s is not IPv4; the proxy listens on IPv4 only", local, remote)
	}
	b := make([]byte, nlmsgHeaderLen+inetDiagReqV2Len)
	ne := binary.NativeEndian
	ne.PutUint32(b[0:], uint32(len(b)))
	ne.PutUint16(b[4:], sockDiagByFamily)
	ne.PutUint16(b[6:], nlmFRequest)
	ne.PutUint32(b[8:], seq)
	req := b[nlmsgHeaderLen:]
	req[0], req[1] = afInet, ipprotoTCP
	ne.PutUint32(req[4:], inetDiagAllStates)
	binary.BigEndian.PutUint16(req[sockIDSportOff:], local.Port())
	binary.BigEndian.PutUint16(req[sockIDSportOff+2:], remote.Port())
	s4, d4 := src.As4(), dst.As4()
	copy(req[sockIDSrcOff:], s4[:])
	copy(req[sockIDDstOff:], d4[:])
	ne.PutUint32(req[sockIDCookieOff:], inetDiagNoCookie)
	ne.PutUint32(req[sockIDCookieOff+4:], inetDiagNoCookie)
	return b, nil
}

// parseSockDiagReply reads the owning uid from the kernel's answer: one
// inet_diag_msg, or an NLMSG_ERROR (ENOENT when no such socket exists).
func parseSockDiagReply(b []byte) (uint32, error) {
	ne := binary.NativeEndian
	if len(b) < nlmsgHeaderLen {
		return 0, fmt.Errorf("sock_diag answer of %d bytes is shorter than a netlink header", len(b))
	}
	switch typ := ne.Uint16(b[4:]); typ {
	case nlmsgError:
		if len(b) < nlmsgHeaderLen+nlmsgErrorCodeSize {
			return 0, errors.New("sock_diag error answer is truncated")
		}
		code := -int32(ne.Uint32(b[nlmsgHeaderLen:]))
		if code == enoent {
			return 0, errSocketNotFound
		}
		return 0, fmt.Errorf("sock_diag refused the query: errno %d", code)
	case sockDiagByFamily:
		msg := b[nlmsgHeaderLen:]
		if len(msg) < inetDiagMsgMinLen {
			return 0, fmt.Errorf("sock_diag answer of %d bytes is shorter than inet_diag_msg", len(msg))
		}
		return ne.Uint32(msg[inetDiagMsgUIDOff:]), nil
	default:
		return 0, fmt.Errorf("sock_diag answered with message type %d", typ)
	}
}

// socketOwnerFunc returns the uid owning the TCP socket bound to local and
// connected to remote.
type socketOwnerFunc func(local, remote netip.AddrPort) (uint32, error)

// ownerOnlyListener accepts only connections dialled by a socket owned by
// uid. Refused connections are closed and reported; Accept keeps waiting.
type ownerOnlyListener struct {
	net.Listener
	uid    uint32
	owner  socketOwnerFunc
	refuse func(error)
}

func (l *ownerOnlyListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if err := l.check(c); err != nil {
			c.Close()
			l.refuse(err)
			continue
		}
		return c, nil
	}
}

// check looks up the dialling socket: bound to the connection's remote
// address, connected to its local one.
func (l *ownerOnlyListener) check(c net.Conn) error {
	client, err := netip.ParseAddrPort(c.RemoteAddr().String())
	if err != nil {
		return fmt.Errorf("refused a connection: parse its address %q: %w", c.RemoteAddr(), err)
	}
	server, err := netip.ParseAddrPort(c.LocalAddr().String())
	if err != nil {
		return fmt.Errorf("refused a connection from %s: parse the listener address %q: %w", client, c.LocalAddr(), err)
	}
	uid, err := l.owner(client, server)
	if err != nil {
		return fmt.Errorf("refused a connection from %s: its owner could not be read: %w", client, err)
	}
	if uid != l.uid {
		return fmt.Errorf("refused a connection from %s: its socket belongs to uid %d, not %d", client, uid, l.uid)
	}
	return nil
}
