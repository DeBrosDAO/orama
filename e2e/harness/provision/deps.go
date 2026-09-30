package provision

import (
	"context"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/agent"
	"github.com/DeBrosOfficial/network/e2e/harness/cloudflare"
	"github.com/DeBrosOfficial/network/e2e/harness/hetzner"
	"github.com/DeBrosOfficial/network/e2e/harness/sshx"
	"github.com/DeBrosOfficial/network/pkg/archivetrust"
	"golang.org/x/crypto/ssh"
)

// Logger receives progress lines. Nothing secret is ever passed to it.
type Logger interface {
	Infof(format string, args ...any)
}

type sshTarget = sshx.Target

// cloud is the Hetzner API as provisioning uses it (*hetzner.Client).
type cloud interface {
	ValidateLocation(ctx context.Context, name string) error
	ValidateServerType(ctx context.Context, name, location string, req hetzner.Requirements) error
	CheckCapacity(ctx context.Context, want, limit int) error
	CreateSSHKey(ctx context.Context, name, publicKey string, labels map[string]string) (*hetzner.SSHKey, error)
	ListSSHKeys(ctx context.Context, selector string) ([]hetzner.SSHKey, error)
	DeleteSSHKey(ctx context.Context, id int64) error
	CreateFirewall(ctx context.Context, name string, rules []hetzner.FirewallRule, labels map[string]string) (*hetzner.Firewall, error)
	ListFirewalls(ctx context.Context, selector string) ([]hetzner.Firewall, error)
	DeleteFirewall(ctx context.Context, id int64) error
	CreateServer(ctx context.Context, o hetzner.CreateServerOpts) (*hetzner.Server, error)
	GetServer(ctx context.Context, id int64) (*hetzner.Server, error)
	ListServers(ctx context.Context, selector string) ([]hetzner.Server, error)
	DeleteServer(ctx context.Context, id int64) error
	WaitRunning(ctx context.Context, id int64, interval time.Duration) (*hetzner.Server, error)
	WaitGone(ctx context.Context, id int64, interval time.Duration) error
}

// dnsZone is the Cloudflare zone as provisioning uses it (*cloudflare.Client).
type dnsZone interface {
	RunSubdomain(runID string) (string, error)
	ClusterSubdomain(runID, label string) (string, error)
	RunIDOf(name string) (string, bool)
	Delegate(ctx context.Context, subdomain string, nss []cloudflare.Nameserver) error
	ListUnder(ctx context.Context, subdomain string) ([]cloudflare.Record, error)
	DeleteUnder(ctx context.Context, subdomain string) ([]cloudflare.Record, error)
	RunRecords(ctx context.Context) ([]cloudflare.Record, error)
	DeleteRecords(ctx context.Context, records []cloudflare.Record) ([]cloudflare.Record, error)
}

// remote runs commands on fleet members and confirms the host key they serve
// is the one generated for them (package sshx).
type remote interface {
	Run(ctx context.Context, t sshTarget, cmd string) (stdout, stderr string, exit int, err error)
	ConfirmHostKey(ctx context.Context, host string, want ssh.PublicKey, interval time.Duration) error
}

// agentRun is a started test agent.
type agentRun struct {
	Dir, Sock, Address string
	Stop               func() error
}

// timing holds every bound provisioning waits under.
type timing struct {
	poll         time.Duration
	serverBoot   time.Duration
	serverGone   time.Duration
	sshReady     time.Duration
	delegation   time.Duration
	certificate  time.Duration
	health       time.Duration
	remoteAction time.Duration
}

func defaultTiming() timing {
	return timing{
		poll:         5 * time.Second,
		serverBoot:   5 * time.Minute,
		serverGone:   3 * time.Minute,
		sshReady:     5 * time.Minute,
		delegation:   3 * time.Minute,
		certificate:  15 * time.Minute,
		health:       10 * time.Minute,
		remoteAction: 2 * time.Minute,
	}
}

// deps is everything with a side effect; tests replace each part.
type deps struct {
	cloud        cloud
	dns          dnsZone
	cmd          commander
	remote       remote
	startAgent   func(ctx context.Context, cfg agent.StartConfig) (*agentRun, error)
	stopAgentDir func(ctx context.Context, dir string) error
	waitCert     func(ctx context.Context, addr, domain, caFile string, interval time.Duration) error
	// verifyArchive checks an archive file is signed by signer.
	verifyArchive func(path, signer string) error
	timing        timing
}

type sshxRemote struct{}

func (sshxRemote) Run(ctx context.Context, t sshTarget, cmd string) (string, string, int, error) {
	return sshx.Run(ctx, t, cmd)
}

func (sshxRemote) ConfirmHostKey(ctx context.Context, host string, want ssh.PublicKey, interval time.Duration) error {
	return sshx.ConfirmHostKey(ctx, host, want, interval)
}

func verifyArchiveFile(path, signer string) error {
	_, err := archivetrust.VerifyArchiveFile(path, []string{signer})
	return err
}

func startRealAgent(ctx context.Context, cfg agent.StartConfig) (*agentRun, error) {
	a, err := agent.Start(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return &agentRun{Dir: a.Dir, Sock: a.Sock, Address: a.Address, Stop: a.Stop}, nil
}

// realDeps wires the real clients from the credentials.
func realDeps(creds credentials) (deps, error) {
	hc, err := hetzner.New(creds.hetznerToken, "")
	if err != nil {
		return deps{}, err
	}
	cf, err := cloudflare.New(creds.cfToken, creds.cfZone, "")
	if err != nil {
		return deps{}, err
	}
	return deps{
		cloud: hc, dns: cf, cmd: execCommander{}, remote: sshxRemote{},
		startAgent: startRealAgent, stopAgentDir: agent.StopDir, waitCert: waitCertificate,
		verifyArchive: verifyArchiveFile, timing: defaultTiming(),
	}, nil
}
