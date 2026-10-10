package provision

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/cloudflare"
	"github.com/DeBrosOfficial/network/e2e/harness/hetzner"
	"golang.org/x/crypto/ssh"
	"gopkg.in/yaml.v3"
)

const testZone = cloudflare.AllowedZone

// fakeCloud is an in-memory Hetzner project.
type fakeCloud struct {
	mu        sync.Mutex
	nextID    int64
	servers   map[int64]hetzner.Server
	keys      map[int64]hetzner.SSHKey
	firewalls map[int64]hetzner.Firewall
	// userData is each server's cloud-init user data, by id.
	userData map[int64]string
	// firewallRules are the rules each firewall was created with, by id.
	firewallRules map[int64][]hetzner.FirewallRule
	// beforeList, when set, runs at the start of the n-th ListServers call.
	beforeList func(n int)
	listCalls  int
	// failCreateAfter makes the n-th CreateServer (1-based) fail; 0 never.
	failCreateAfter int
	creates         int
	failList        bool
	capacityErr     error
	// capacityLimits records the limit of every CheckCapacity call.
	capacityLimits []int
	// ctxAware makes GetServer and DeleteServer fail on an ended context,
	// as the real client does.
	ctxAware bool
	// keysSurviveDelete makes DeleteSSHKey answer success and keep the key.
	keysSurviveDelete bool
}

func newFakeCloud() *fakeCloud {
	return &fakeCloud{servers: map[int64]hetzner.Server{}, keys: map[int64]hetzner.SSHKey{}, firewalls: map[int64]hetzner.Firewall{},
		userData: map[int64]string{}, firewallRules: map[int64][]hetzner.FirewallRule{}}
}

func (f *fakeCloud) id() int64 { f.nextID++; return f.nextID }

func selects(selector string, labels map[string]string) bool {
	if selector == "" {
		return true
	}
	k, v, hasValue := strings.Cut(selector, "=")
	got, ok := labels[k]
	return ok && (!hasValue || got == v)
}

func (f *fakeCloud) ValidateLocation(context.Context, string) error { return nil }
func (f *fakeCloud) ValidateServerType(context.Context, string, string, hetzner.Requirements) error {
	return nil
}
func (f *fakeCloud) CheckCapacity(_ context.Context, _, limit int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.capacityLimits = append(f.capacityLimits, limit)
	return f.capacityErr
}

func (f *fakeCloud) CreateSSHKey(_ context.Context, name, _ string, labels map[string]string) (*hetzner.SSHKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := hetzner.SSHKey{ID: f.id(), Name: name, Labels: labels, Created: time.Now()}
	f.keys[k.ID] = k
	return &k, nil
}

func (f *fakeCloud) ListSSHKeys(_ context.Context, sel string) ([]hetzner.SSHKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []hetzner.SSHKey
	for _, k := range f.keys {
		if selects(sel, k.Labels) {
			out = append(out, k)
		}
	}
	return out, nil
}

func (f *fakeCloud) DeleteSSHKey(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.keysSurviveDelete {
		delete(f.keys, id)
	}
	return nil
}

func (f *fakeCloud) CreateFirewall(_ context.Context, name string, rules []hetzner.FirewallRule, labels map[string]string) (*hetzner.Firewall, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fw := hetzner.Firewall{ID: f.id(), Name: name, Labels: labels, Created: time.Now()}
	f.firewalls[fw.ID] = fw
	f.firewallRules[fw.ID] = rules
	return &fw, nil
}

func (f *fakeCloud) ListFirewalls(_ context.Context, sel string) ([]hetzner.Firewall, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []hetzner.Firewall
	for _, fw := range f.firewalls {
		if selects(sel, fw.Labels) {
			out = append(out, fw)
		}
	}
	return out, nil
}

func (f *fakeCloud) DeleteFirewall(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.servers {
		if s.Labels[hetzner.LabelRun] != "" && f.firewalls[id].Labels[hetzner.LabelRun] == s.Labels[hetzner.LabelRun] {
			return errors.New("resource_in_use: firewall still applied")
		}
	}
	delete(f.firewalls, id)
	return nil
}

func (f *fakeCloud) CreateServer(_ context.Context, o hetzner.CreateServerOpts) (*hetzner.Server, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.creates++
	if f.failCreateAfter != 0 && f.creates >= f.failCreateAfter {
		return nil, errors.New("hetzner: resource_limit_exceeded")
	}
	s := hetzner.Server{ID: f.id(), Name: o.Name, Status: "initializing", Labels: o.Labels, Created: time.Now()}
	s.PublicNet.IPv4.IP = fmt.Sprintf("203.0.113.%d", s.ID)
	f.servers[s.ID] = s
	f.userData[s.ID] = o.UserData
	return &s, nil
}

func (f *fakeCloud) addServer(name string, labels map[string]string, created time.Time) hetzner.Server {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := hetzner.Server{ID: f.id(), Name: name, Status: "running", Labels: labels, Created: created}
	f.servers[s.ID] = s
	return s
}

func (f *fakeCloud) ListServers(_ context.Context, sel string) ([]hetzner.Server, error) {
	f.mu.Lock()
	f.listCalls++
	n, hook := f.listCalls, f.beforeList
	f.mu.Unlock()
	if hook != nil {
		hook(n)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failList {
		return nil, errors.New("hetzner: HTTP 503")
	}
	var out []hetzner.Server
	for _, s := range f.servers {
		if selects(sel, s.Labels) {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *fakeCloud) DeleteServer(ctx context.Context, id int64) error {
	if f.ctxAware && ctx.Err() != nil {
		return ctx.Err()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.servers, id)
	return nil
}

func (f *fakeCloud) WaitRunning(_ context.Context, id int64, _ time.Duration) (*hetzner.Server, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.servers[id]
	if !ok {
		return nil, fmt.Errorf("server %d gone", id)
	}
	s.Status = "running"
	f.servers[id] = s
	return &s, nil
}

func (f *fakeCloud) WaitGone(_ context.Context, id int64, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.servers[id]; ok {
		return fmt.Errorf("server %d still there", id)
	}
	return nil
}

func (f *fakeCloud) counts() (servers, keys, firewalls int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.servers), len(f.keys), len(f.firewalls)
}

// hostKeyOf is the ed25519 host key the user data of the server at ip
// installs, as cloud-init would.
func (f *fakeCloud) hostKeyOf(ip string) (ssh.PublicKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, s := range f.servers {
		if s.IPv4() != ip {
			continue
		}
		var cc hostKeyCloudConfig
		if err := yaml.Unmarshal([]byte(f.userData[id]), &cc); err != nil {
			return nil, fmt.Errorf("server %d user data: %w", id, err)
		}
		key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(cc.SSHKeys["ed25519_public"]))
		if err != nil {
			return nil, fmt.Errorf("server %d has no ed25519 host key in its user data: %w", id, err)
		}
		return key, nil
	}
	return nil, fmt.Errorf("no server at %s", ip)
}

// fakeRemote answers host key confirmations from the fake cloud's user data
// and the wg0 query.
type fakeRemote struct {
	mu     sync.Mutex
	cloud  *fakeCloud
	cmds   []string
	exit   int
	stderr string
	wgIP   func(host string) string
	// scanErr makes every sshd unreachable; foreignKey makes every sshd
	// present a key other than the one its user data installed.
	scanErr    error
	foreignKey bool
}

func (r *fakeRemote) Run(_ context.Context, t sshTarget, cmd string) (string, string, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cmds = append(r.cmds, t.Host+": "+cmd)
	if cmd == wgAddrCommand {
		return "4: wg0    inet " + r.wgIP(t.Host) + "/24 scope global wg0\n", "", 0, nil
	}
	return "", r.stderr, r.exit, nil
}

func (r *fakeRemote) ConfirmHostKey(_ context.Context, host string, want ssh.PublicKey, _ time.Duration) error {
	if r.scanErr != nil {
		return r.scanErr
	}
	served, err := r.cloud.hostKeyOf(host)
	if err != nil {
		return err
	}
	if r.foreignKey {
		pub, _, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		if served, err = ssh.NewPublicKey(pub); err != nil {
			return err
		}
	}
	if !bytes.Equal(served.Marshal(), want.Marshal()) {
		return fmt.Errorf("%s presented host key %s, not the pinned %s", host, ssh.FingerprintSHA256(served), ssh.FingerprintSHA256(want))
	}
	return nil
}

// wgFromPublic maps 203.0.113.N to 10.0.0.N.
func wgFromPublic(host string) string {
	return "10.0.0." + host[strings.LastIndex(host, ".")+1:]
}
