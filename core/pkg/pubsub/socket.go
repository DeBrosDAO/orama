package pubsub

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"

	"go.uber.org/zap"
)

// The pubsub HTTP API used to listen on 127.0.0.1:10105 with nothing in front
// of it. Every process on the node is on loopback — a tenant's deployment
// included — and the API takes the namespace from the request, so any of them
// could publish into, or subscribe to, any namespace's topics.
//
// It listens on a unix socket instead, in the unit's own runtime directory
// (RuntimeDirectory=orama-pubsub, 0700), itself 0600, and it accepts a
// connection only when the kernel says the process on the other end runs as
// the same user as the pubsub service: the gateways that front it. The
// directory and file modes keep everyone else from connecting at all; the peer
// check does not depend on them being right.

// DefaultSocketPath is where orama-namespace-pubsub@index serves its API and
// where every gateway on the node reaches it.
const DefaultSocketPath = "/run/orama-pubsub/pubsub.sock"

// socketMode is the socket file's mode.
const socketMode = 0o600

// ListenSocket listens on the unix socket at path and admits only connections
// from processes running as this process's uid. A socket left by a previous
// run is replaced; any other file at path is an error rather than something to
// delete.
func ListenSocket(path string, logger *zap.Logger) (net.Listener, error) {
	if logger == nil {
		logger = zap.NewNop()
	}
	if err := removeStaleSocket(path); err != nil {
		return nil, err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen on the pubsub socket %s: %w", path, err)
	}
	if err := os.Chmod(path, socketMode); err != nil {
		ln.Close()
		return nil, fmt.Errorf("restrict the pubsub socket %s to its owner: %w", path, err)
	}
	return &peerCredListener{Listener: ln, uid: uint32(os.Getuid()), peerUID: peerUID, logger: logger}, nil
}

func removeStaleSocket(path string) error {
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("inspect %s before listening: %w", path, err)
	case info.Mode()&fs.ModeSocket == 0:
		return fmt.Errorf("%s exists and is not a socket; refusing to replace it", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove the stale pubsub socket %s: %w", path, err)
	}
	return nil
}

// peerCredListener drops every connection whose peer is not uid.
type peerCredListener struct {
	net.Listener
	uid     uint32
	peerUID func(*net.UnixConn) (uint32, error)
	logger  *zap.Logger
}

func (l *peerCredListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if err := l.admit(conn); err != nil {
			l.logger.Warn("refused a pubsub API connection", zap.Error(err))
			conn.Close()
			continue
		}
		return conn, nil
	}
}

func (l *peerCredListener) admit(conn net.Conn) error {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return fmt.Errorf("a %T is not a unix socket connection", conn)
	}
	uid, err := l.peerUID(uc)
	if err != nil {
		return fmt.Errorf("cannot identify the caller: %w", err)
	}
	if uid != l.uid {
		return fmt.Errorf("caller uid %d is not %d, the uid this service runs as", uid, l.uid)
	}
	return nil
}
