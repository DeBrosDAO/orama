// Package sshx runs commands on and copies files to the servers of an e2e run.
//
// Every connection authenticates with the run's own private key and checks the
// server's host key strictly against the run's known_hosts file: a host that is
// not in it, or presents another key, is refused. Only ssh-ed25519 host keys
// are negotiated, the one kind the provisioner pins (see ConfirmHostKey).
package sshx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

const (
	// defaultPort is where sshd listens when Target.Host names no port.
	defaultPort = "22"
	// dialTimeout bounds the TCP connect and the SSH handshake.
	dialTimeout = 15 * time.Second
	// maxOutputBytes caps what Run keeps of stdout and of stderr.
	maxOutputBytes = 16 << 20
	// maxGetBytes caps the size of a file Get reads.
	maxGetBytes = 64 << 20
	// exitUnknown is the exit status Run reports when the command never ran.
	exitUnknown = -1
)

// Target is one server as the harness reaches it.
type Target struct {
	// Host is an IP or name, optionally with :port (default 22).
	Host string
	// User is the login, root on Hetzner images.
	User string
	// KeyFile is the private key that opens the server.
	KeyFile string
	// KnownHostsFile holds the pinned host key; nothing else is trusted.
	KnownHostsFile string
}

// safeRemotePath is an absolute path of plain characters: it is embedded in a
// remote shell command, so nothing a shell would interpret may be in it.
var safeRemotePath = regexp.MustCompile(`^/[A-Za-z0-9._/-]+$`)

// hostKeyAlgorithms is the only host key kind negotiated and pinned.
var hostKeyAlgorithms = []string{ssh.KeyAlgoED25519}

// Run executes cmd on t. A command that ran and exited non-zero is not an
// error: its status is in exit and err is nil. err is set when the command
// could not be run at all (dial, host key, auth, context), with exit -1.
func Run(ctx context.Context, t Target, cmd string) (stdout, stderr string, exit int, err error) {
	var out, errOut limitedBuffer
	out.limit, errOut.limit = maxOutputBytes, maxOutputBytes
	exit, err = run(ctx, t, cmd, nil, &out, &errOut)
	return out.String(), errOut.String(), exit, err
}

// Put writes data to remotePath with mode, atomically: into a temp file in the
// same directory (created under umask 077), then renamed over remotePath.
func Put(ctx context.Context, t Target, remotePath string, data []byte, mode os.FileMode) error {
	if err := checkRemotePath(remotePath); err != nil {
		return err
	}
	dir := remotePath[:strings.LastIndex(remotePath, "/")+1]
	cmd := fmt.Sprintf(`umask 077 && tmp=$(mktemp '%s.sshx.XXXXXX') && cat > "$tmp" && chmod %04o "$tmp" && mv -f "$tmp" '%s'`,
		dir, mode.Perm(), remotePath)
	var errOut limitedBuffer
	errOut.limit = maxOutputBytes
	exit, err := run(ctx, t, cmd, bytes.NewReader(data), &limitedBuffer{limit: maxOutputBytes}, &errOut)
	if err != nil {
		return fmt.Errorf("failed to write %s on %s: %w", remotePath, t.Host, err)
	}
	if exit != 0 {
		return fmt.Errorf("failed to write %s on %s: exit %d: %s", remotePath, t.Host, exit, strings.TrimSpace(errOut.String()))
	}
	return nil
}

// Get reads remotePath from t. Files over 64 MiB are refused.
func Get(ctx context.Context, t Target, remotePath string) ([]byte, error) {
	if err := checkRemotePath(remotePath); err != nil {
		return nil, err
	}
	out := limitedBuffer{limit: maxGetBytes}
	errOut := limitedBuffer{limit: maxOutputBytes}
	exit, err := run(ctx, t, fmt.Sprintf("cat -- '%s'", remotePath), nil, &out, &errOut)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s on %s: %w", remotePath, t.Host, err)
	}
	if exit != 0 {
		return nil, fmt.Errorf("failed to read %s on %s: exit %d: %s", remotePath, t.Host, exit, strings.TrimSpace(errOut.String()))
	}
	if out.overflow {
		return nil, fmt.Errorf("failed to read %s on %s: larger than %d bytes", remotePath, t.Host, maxGetBytes)
	}
	return out.Bytes(), nil
}

// checkRemotePath refuses a path that is not absolute, has a '..' segment, or
// holds a character a remote shell would interpret.
func checkRemotePath(p string) error {
	if !safeRemotePath.MatchString(p) || strings.HasSuffix(p, "/") {
		return fmt.Errorf("remote path %q must be an absolute file path of letters, digits, '.', '_', '-' and '/'", p)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return fmt.Errorf("remote path %q must not contain '..'", p)
		}
	}
	return nil
}

// run executes cmd in one session, closing the connection when ctx ends.
func run(ctx context.Context, t Target, cmd string, stdin *bytes.Reader, stdout, stderr *limitedBuffer) (int, error) {
	client, err := dial(ctx, t)
	if err != nil {
		return exitUnknown, err
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return exitUnknown, fmt.Errorf("failed to open an SSH session on %s: %w", t.Host, err)
	}
	defer session.Close()
	if stdin != nil {
		session.Stdin = stdin
	}
	session.Stdout, session.Stderr = stdout, stderr

	done := make(chan error, 1)
	go func() { done <- session.Run(cmd) }()
	select {
	case <-ctx.Done():
		// Closing the connection ends session.Run; wait for it so nothing
		// writes into the buffers after this returns.
		client.Close()
		<-done
		return exitUnknown, fmt.Errorf("command on %s interrupted: %w", t.Host, ctx.Err())
	case err := <-done:
		return exitStatus(t.Host, err)
	}
}

// exitStatus turns session.Run's error into an exit status.
func exitStatus(host string, err error) (int, error) {
	if err == nil {
		return 0, nil
	}
	var ee *ssh.ExitError
	if errors.As(err, &ee) {
		return ee.ExitStatus(), nil
	}
	return exitUnknown, fmt.Errorf("command on %s did not report an exit status: %w", host, err)
}

// dial connects to t with its key, checking the host key against its
// known_hosts file only.
func dial(ctx context.Context, t Target) (*ssh.Client, error) {
	if t.KeyFile == "" || t.KnownHostsFile == "" || t.User == "" || t.Host == "" {
		return nil, fmt.Errorf("SSH target %q needs Host, User, KeyFile and KnownHostsFile", t.Host)
	}
	raw, err := os.ReadFile(t.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read SSH key %s: %w", t.KeyFile, err)
	}
	signer, err := ssh.ParsePrivateKey(raw)
	if err != nil {
		return nil, fmt.Errorf("failed to parse SSH key %s: %w", t.KeyFile, err)
	}
	check, err := knownhosts.New(t.KnownHostsFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load known_hosts %s: %w", t.KnownHostsFile, err)
	}
	cfg := &ssh.ClientConfig{
		User:              t.User,
		Auth:              []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback:   check,
		HostKeyAlgorithms: hostKeyAlgorithms,
		Timeout:           dialTimeout,
	}
	return connect(ctx, address(t.Host), cfg)
}

// connect dials addr and runs the handshake, both bounded by dialTimeout and ctx.
func connect(ctx context.Context, addr string, cfg *ssh.ClientConfig) (*ssh.Client, error) {
	dctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(dctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to %s: %w", addr, err)
	}
	if deadline, ok := dctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			conn.Close()
			return nil, fmt.Errorf("failed to set the handshake deadline for %s: %w", addr, err)
		}
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("SSH handshake with %s failed: %w", addr, err)
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		c.Close()
		return nil, fmt.Errorf("failed to clear the handshake deadline for %s: %w", addr, err)
	}
	return ssh.NewClient(c, chans, reqs), nil
}

// address adds the default port to a host that names none.
func address(host string) string {
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host
	}
	return net.JoinHostPort(host, defaultPort)
}

// limitedBuffer keeps the first limit bytes written and drops the rest.
type limitedBuffer struct {
	bytes.Buffer
	limit    int
	overflow bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	room := b.limit - b.Len()
	if room <= 0 {
		b.overflow = b.overflow || len(p) > 0
		return len(p), nil
	}
	if len(p) > room {
		b.overflow = true
		b.Buffer.Write(p[:room])
		return len(p), nil
	}
	b.Buffer.Write(p)
	return len(p), nil
}
