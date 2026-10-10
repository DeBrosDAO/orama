package setup

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	psetup "github.com/DeBrosOfficial/network/cmd/orama/internal/production/setup"
	"github.com/DeBrosOfficial/network/pkg/archivetrust"
	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/DeBrosOfficial/network/pkg/remotessh"
	"github.com/DeBrosOfficial/network/pkg/rollout"
)

// stderrKept is how much of a failed command's stderr an error carries.
const stderrKept = 2048

// shell runs commands on one machine. The real one is SSH; the tests' records.
type shell interface {
	// Run runs command with stdin (may be nil); its stdout goes to out (nil
	// discards it). A non-zero exit is an error carrying the end of stderr.
	Run(ctx context.Context, command string, stdin io.Reader, out io.Writer) error
	// Upload copies a local file to a path on the machine.
	Upload(local, remote string) error
}

// sshShell is a machine reached with the operator's RootWallet key.
type sshShell struct{ node inspector.Node }

func (s sshShell) Run(ctx context.Context, command string, stdin io.Reader, out io.Writer) error {
	cmd, err := remotessh.Command(ctx, s.node, command)
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, out, &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run on %s: %w: %s", s.node.Host, err, tail(stderr.String(), stderrKept))
	}
	return nil
}

func (s sshShell) Upload(local, remote string) error {
	return remotessh.UploadFile(s.node, local, remote)
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return "..." + s[len(s)-n:]
	}
	return s
}

// lineWriter turns the bytes a command prints into report lines.
type lineWriter struct {
	prefix string
	report Reporter
	buf    []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			return len(p), nil
		}
		if line := strings.TrimRight(string(w.buf[:i]), "\r"); strings.TrimSpace(line) != "" {
			w.report.Linef("  [%s] %s", w.prefix, line)
		}
		w.buf = w.buf[i+1:]
	}
}

// sshMachine is a VPS setup drives over SSH.
type sshMachine struct {
	sh     shell
	node   inspector.Node
	wallet string
	report Reporter
	close  func()

	// Seams for tests.
	ensureArchive func(node inspector.Node, archive string, trusted []string) error
	waitReady     func(node inspector.Node, budget time.Duration) error
	startTunnel   func(ctx context.Context, node inspector.Node, remote string) (string, func(), error)
}

func newSSHMachine(e *psetup.Enrolled, wallet string, report Reporter) *sshMachine {
	return &sshMachine{
		sh: sshShell{node: e.Node}, node: e.Node, wallet: wallet, report: report, close: e.Close,
		ensureArchive: psetup.EnsureArchive,
		waitReady: func(node inspector.Node, budget time.Duration) error {
			return rollout.WaitReady(node, rollout.DefaultRunner, budget)
		},
		startTunnel: remotessh.StartTunnel,
	}
}

func (m *sshMachine) Host() string { return m.node.Host }
func (m *sshMachine) Close()       { m.close() }

func (m *sshMachine) sudo() string { return remotessh.SudoPrefix(m.node) }

func (m *sshMachine) logs() io.Writer { return &lineWriter{prefix: m.node.Host, report: m.report} }

// capture runs command and returns its stdout.
func (m *sshMachine) capture(ctx context.Context, command string, stdin io.Reader) (string, error) {
	var out bytes.Buffer
	err := m.sh.Run(ctx, command, stdin, &out)
	return out.String(), err
}

// Probe reads the machine's hardware and what it has installed.
func (m *sshMachine) Probe(ctx context.Context) (Facts, error) {
	out, err := m.capture(ctx, bash(m.sudo(), probeScript), nil)
	if err != nil {
		return Facts{}, err
	}
	return ParseFacts(out)
}

// StageRelease verifies the endorsed release against the operator's wallet,
// uploads it and puts it in place at /opt/orama, the way `orama node setup` does.
func (m *sshMachine) StageRelease(_ context.Context, rel *Release) error {
	return m.ensureArchive(m.node, rel.ArchivePath, []string{m.wallet})
}

// InstallCluster runs `orama node install` for the cluster node.
func (m *sshMachine) InstallCluster(ctx context.Context, in ClusterInstall) error {
	role := "node"
	if in.Domain != "" {
		role = "nameserver"
	}
	opts := psetup.Options{IP: in.IP, Env: in.Env, Role: role, User: in.User, BaseDomain: in.Domain, Genesis: in.Create, ACMECA: in.ACMECA}
	command := psetup.InstallCommand(opts, in.Wallet, in.Signers, in.Invite)
	secrets, err := psetup.InstallSecrets(in.Invite)
	if err != nil {
		return err
	}
	var stdin io.Reader
	if len(secrets) > 0 {
		stdin = bytes.NewReader(secrets)
	}
	return m.sh.Run(ctx, command, stdin, m.logs())
}

// MintInvite mints an invite for a new node on this machine, which is in the
// cluster, and reads the archive signers a joiner must expect from it.
func (m *sshMachine) MintInvite(ctx context.Context) (string, []string, error) {
	out, err := m.capture(ctx, psetup.MintInviteCommand(), nil)
	if err != nil {
		return "", nil, err
	}
	invite, err := psetup.ParseMintedInvite(out)
	if err != nil {
		return "", nil, err
	}
	anchor, err := m.capture(ctx, "cat "+archivetrust.AnchorPath, nil)
	if err != nil {
		return "", nil, fmt.Errorf("read the archive trust anchor: %w", err)
	}
	signers, err := archivetrust.ParseAnchor([]byte(anchor))
	if err != nil {
		return "", nil, fmt.Errorf("the archive trust anchor of %s: %w", m.node.Host, err)
	}
	return invite, signers, nil
}

// WaitNode waits until the cluster node carries its share of the cluster.
func (m *sshMachine) WaitNode(_ context.Context, budget time.Duration) error {
	return m.waitReady(m.node, budget)
}

// RestartNode restarts the cluster node and waits for it.
func (m *sshMachine) RestartNode(ctx context.Context, budget time.Duration, force bool) error {
	command := m.sudo() + orama + " node restart"
	if force {
		command += " --force"
	}
	if err := m.sh.Run(ctx, command, nil, m.logs()); err != nil {
		return err
	}
	return m.WaitNode(ctx, budget)
}

// sshEnroller reaches machines with the operator's RootWallet.
type sshEnroller struct {
	wallet Wallet
	report Reporter
}

// Enroll gives the RootWallet an SSH key on the machine and proves it opens it.
func (e sshEnroller) Enroll(ctx context.Context, req MachineRequest) (Machine, error) {
	evm, err := e.wallet.EVMAddress(ctx)
	if err != nil {
		return nil, err
	}
	enrolled, err := psetup.Enroll(psetup.EnrollRequest{
		IP: req.IP, User: req.User, UsePassword: req.UsePassword, Password: req.Password,
		BootstrapKey: req.BootstrapKey, HostKey: req.HostKey, Role: "node", Env: req.Env,
	})
	if err != nil {
		return nil, err
	}
	return newSSHMachine(enrolled, evm, e.report), nil
}
