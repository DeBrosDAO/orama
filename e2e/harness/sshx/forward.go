package sshx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"

	"golang.org/x/crypto/ssh"
)

// loopbackAny is where both ends of a forward listen: the runner's loopback
// for a local forward, the node's for a remote one (sshd binds a remote
// forward to loopback unless GatewayPorts says otherwise).
const loopbackAny = "127.0.0.1:0"

// Forward is an open SSH port forward over one connection with the run's
// key and pinned host key. Close ends it and every connection through it.
type Forward struct {
	// Addr is the listening end: on this machine for LocalForward, on the
	// node for RemoteForward ("127.0.0.1:<port>").
	Addr string

	client *ssh.Client
	ln     net.Listener
	wg     sync.WaitGroup
}

// LocalForward listens on a loopback port of this machine and carries every
// connection to it to remoteAddr as the node t dials it ("127.0.0.1:31003"
// is the node's own loopback), like `ssh -L`.
func LocalForward(ctx context.Context, t Target, remoteAddr string) (*Forward, error) {
	client, err := dial(ctx, t)
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", loopbackAny)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("failed to listen for the forward to %s on %s: %w", remoteAddr, t.Host, err), client.Close())
	}
	f := &Forward{Addr: ln.Addr().String(), client: client, ln: ln}
	f.serve(func() (net.Conn, error) { return client.Dial("tcp", remoteAddr) })
	return f, nil
}

// RemoteForward asks the node t to listen on a loopback port of its own and
// carries every connection to it back to localAddr on this machine, like
// `ssh -R`: a server the test runs becomes reachable from the node at
// Forward.Addr.
func RemoteForward(ctx context.Context, t Target, localAddr string) (*Forward, error) {
	client, err := dial(ctx, t)
	if err != nil {
		return nil, err
	}
	ln, err := client.Listen("tcp", loopbackAny)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("%s refused a remote forward (is AllowTcpForwarding on?): %w", t.Host, err), client.Close())
	}
	f := &Forward{Addr: ln.Addr().String(), client: client, ln: ln}
	f.serve(func() (net.Conn, error) { return (&net.Dialer{Timeout: dialTimeout}).Dial("tcp", localAddr) })
	return f, nil
}

// serve accepts on f.ln and pipes each connection to what open returns.
func (f *Forward) serve(open func() (net.Conn, error)) {
	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		for {
			in, err := f.ln.Accept()
			if err != nil {
				return
			}
			f.wg.Add(1)
			go func() {
				defer f.wg.Done()
				pipe(in, open)
			}()
		}
	}()
}

// pipe copies in to the connection open returns and back until either side
// closes. A far end that cannot be reached closes in at once: the client
// sees a reset, as with ssh -L.
func pipe(in net.Conn, open func() (net.Conn, error)) {
	defer in.Close()
	out, err := open()
	if err != nil {
		return
	}
	defer out.Close()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(out, in); done <- struct{}{} }()
	go func() { _, _ = io.Copy(in, out); done <- struct{}{} }()
	<-done
}

// Close stops listening, closes the SSH connection (and with it every
// forwarded connection) and waits for the forwarding goroutines.
func (f *Forward) Close() error {
	lnErr := f.ln.Close()
	clientErr := f.client.Close()
	f.wg.Wait()
	var errs []error
	if lnErr != nil && !errors.Is(lnErr, net.ErrClosed) && !errors.Is(lnErr, io.EOF) {
		errs = append(errs, fmt.Errorf("failed to close the forward listener %s: %w", f.Addr, lnErr))
	}
	if clientErr != nil && !errors.Is(clientErr, net.ErrClosed) {
		errs = append(errs, fmt.Errorf("failed to close the forward's SSH connection: %w", clientErr))
	}
	return errors.Join(errs...)
}
