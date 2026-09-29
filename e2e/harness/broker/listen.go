package broker

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/DeBrosOfficial/network/e2e/harness/secrets"
)

// Socket placement: SocketPath(workDir) is <work dir>/broker/broker.sock.
const (
	dirName    = "broker"
	sockName   = "broker.sock"
	dirMode    = 0o700
	socketMode = 0o600
	// maxSockPath is the shortest sun_path limit of the platforms the runner
	// runs on (104 bytes on macOS, 108 on Linux), minus the NUL.
	maxSockPath = 103
)

// SocketPath is where the run whose work dir is workDir serves its broker.
func SocketPath(workDir string) string {
	return filepath.Join(workDir, dirName, sockName)
}

// Listener is a serving broker.
type Listener struct {
	Path   string
	ln     net.Listener
	cancel context.CancelFunc
	wg     sync.WaitGroup
	mu     sync.Mutex
	errs   []error
}

// Listen serves s on SocketPath(workDir) until Close. The socket's directory
// is created 0700 (and must not be anyone else's), the socket is 0600, and
// a stale socket left by a crashed runner is replaced. The process serving
// it must not itself be a broker client: a runner started with
// E2E_BROKER_SOCK set is refused, so an operation can never loop back.
func Listen(ctx context.Context, workDir string, s *Server, lookup func(string) (string, bool)) (*Listener, error) {
	if v, ok := lookup(EnvSock); ok && v != "" {
		return nil, fmt.Errorf("refusing to serve a broker from a process that has %s set (a feature process?)", EnvSock)
	}
	if s.State == nil || s.DNS == nil || s.Cloud == nil {
		return nil, errors.New("a broker needs the run's state, a DNS zone and a cloud")
	}
	path := SocketPath(workDir)
	if len(path) > maxSockPath {
		return nil, fmt.Errorf("broker socket path %s is %d bytes, over the %d a unix socket allows: use a shorter E2E_WORK_DIR", path, len(path), maxSockPath)
	}
	if err := prepareDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on the broker socket %s: %w", path, err)
	}
	if err := os.Chmod(path, socketMode); err != nil {
		return nil, errors.Join(fmt.Errorf("failed to restrict the broker socket %s: %w", path, err), ln.Close())
	}
	ctx, cancel := context.WithCancel(ctx)
	l := &Listener{Path: path, ln: ln, cancel: cancel}
	l.wg.Add(1)
	go l.accept(ctx, s)
	return l, nil
}

// prepareDir creates dir 0700, refuses one with other permissions, and
// removes a stale socket in it.
func prepareDir(dir string) error {
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return fmt.Errorf("failed to create the broker dir %s: %w", dir, err)
	}
	if err := secrets.CheckOwnedDir(dir, dirMode); err != nil {
		return fmt.Errorf("the broker dir: %w", err)
	}
	sock := filepath.Join(dir, sockName)
	st, err := os.Lstat(sock)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to inspect %s: %w", sock, err)
	}
	if st.Mode()&fs.ModeSocket == 0 {
		return fmt.Errorf("%s exists and is not a socket; refusing to replace it", sock)
	}
	if err := os.Remove(sock); err != nil {
		return fmt.Errorf("failed to remove the stale broker socket %s: %w", sock, err)
	}
	return nil
}

func (l *Listener) accept(ctx context.Context, s *Server) {
	defer l.wg.Done()
	slots := make(chan struct{}, maxConns)
	for {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			return
		}
		conn, err := l.ln.Accept()
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				l.fail(fmt.Errorf("broker accept: %w", err))
			}
			return
		}
		l.wg.Add(1)
		go func() {
			defer func() { <-slots; l.wg.Done() }()
			if err := s.serveConn(ctx, conn); err != nil {
				l.fail(err)
			}
		}()
	}
}

func (l *Listener) fail(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.errs = append(l.errs, err)
}

// Close stops accepting, cancels running operations, waits for them and
// removes the socket. It returns every serving error seen.
func (l *Listener) Close() error {
	l.cancel()
	closeErr := l.ln.Close()
	l.wg.Wait()
	l.mu.Lock()
	defer l.mu.Unlock()
	errs := append([]error{}, l.errs...)
	if closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
		errs = append(errs, fmt.Errorf("failed to close the broker socket: %w", closeErr))
	}
	if err := os.Remove(l.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		errs = append(errs, fmt.Errorf("failed to remove the broker socket %s: %w", l.Path, err))
	}
	return errors.Join(errs...)
}
