package setup

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/dnsdelegation"
	"github.com/DeBrosOfficial/network/pkg/clusterreg"
	"github.com/DeBrosOfficial/network/pkg/install"
	"github.com/DeBrosOfficial/network/pkg/netregistry"
	"github.com/DeBrosOfficial/network/pkg/onchain"
	"github.com/DeBrosOfficial/network/pkg/statesync"
)

const (
	testOperator = "orama1fvfzzvqv2ara2crn3z352zjhnfl0tw4rk82j53"
	testEVM      = "0x1111111111111111111111111111111111111111"
	testChainID  = "orama-stagenet-6"
	testRootSHA  = "aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11"
	testManifest = "bb22bb22bb22bb22bb22bb22bb22bb22bb22bb22bb22bb22bb22bb22bb22bb22"
	testSeedHash = "cc33cc33cc33cc33cc33cc33cc33cc33cc33cc33cc33cc33cc33cc33cc33cc33"
	testCLISHA   = "dd44dd44dd44dd44dd44dd44dd44dd44dd44dd44dd44dd44dd44dd44dd44dd44"
)

// world is the shared state of the fakes: an ordered log of everything the run
// did, and the chain's registry.
type world struct {
	mu  sync.Mutex
	log []string

	// registry
	operatorRegistered bool
	nodes              map[string]*RegisteredNode
	validator          bool
	balance            *big.Int
	faucetPays         *big.Int
	faucetCalls        int

	// concurrency probe for restarts
	restarting, maxRestarting int
}

func newWorld() *world {
	return &world{nodes: map[string]*RegisteredNode{}, balance: new(big.Int)}
}

func (w *world) add(format string, args ...any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.log = append(w.log, fmt.Sprintf(format, args...))
}

func (w *world) entries() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.log...)
}

// index is the position of the first log entry with the prefix, -1 if none.
func (w *world) index(prefix string) int {
	for i, e := range w.entries() {
		if strings.HasPrefix(e, prefix) {
			return i
		}
	}
	return -1
}

func (w *world) count(prefix string) int {
	n := 0
	for _, e := range w.entries() {
		if strings.HasPrefix(e, prefix) {
			n++
		}
	}
	return n
}

// fakeMachine is one VPS.
type fakeMachine struct {
	w     *world
	ip    string
	facts Facts

	installClusterErr error
	// quorumRefusal, when set, is what the node's own quorum check answers to a
	// restart without --force.
	quorumRefusal string
	state         ChainState
	stateErr      error
	waitErr       error
	installed     ClusterInstall
	globalIn      GlobalInstall
	identityErr   error
}

func goodHardware() install.Hardware {
	return install.Hardware{CPUCores: 8, RAMBytes: 16 << 30, FreeDiskBytes: 400 << 30}
}

func freshFacts() Facts { return Facts{Arch: "amd64", Hardware: goodHardware()} }

func (m *fakeMachine) Host() string { return m.ip }
func (m *fakeMachine) Probe(context.Context) (Facts, error) {
	m.w.add("probe %s", m.ip)
	return m.facts, nil
}
func (m *fakeMachine) StageRelease(_ context.Context, rel *Release) error {
	m.w.add("stage %s %s", m.ip, rel.Version)
	return nil
}
func (m *fakeMachine) InstallCluster(_ context.Context, in ClusterInstall) error {
	m.installed = in
	kind := "join"
	if in.Create {
		kind = "create"
	}
	m.w.add("cluster %s %s", m.ip, kind)
	return m.installClusterErr
}
func (m *fakeMachine) MintInvite(context.Context) (string, []string, error) {
	m.w.add("invite on %s", m.ip)
	return "orama1_invite", []string{testEVM}, nil
}
func (m *fakeMachine) InstallGlobal(_ context.Context, in GlobalInstall) error {
	m.globalIn = in
	m.w.add("global %s %s", m.ip, strings.Join(in.Node.ServiceNames(), ","))
	return nil
}
func (m *fakeMachine) StartGlobal(_ context.Context, n NodePlan) error {
	m.w.add("startglobal %s %s", m.ip, strings.Join(n.ServiceNames(), ","))
	return nil
}
func (m *fakeMachine) ChainState(context.Context) (ChainState, error) {
	m.w.add("chainstate %s", m.ip)
	return m.state, m.stateErr
}
func (m *fakeMachine) Identity(_ context.Context, in IdentityRequest) (NodeIdentity, error) {
	m.w.add("identity %s", m.ip)
	return NodeIdentity{
		ChainNodeID: strings.Repeat("ab", 20), ConsensusPubKey: make([]byte, 32), HotKey: "orama1hot" + m.ip,
		HotBinding: clusterreg.NodeBinding{Service: clusterreg.HotKeyService, KeyType: "secp256k1"},
	}, m.identityErr
}
func (m *fakeMachine) StartServices(_ context.Context, id string) error {
	m.w.add("start %s %s", m.ip, id)
	return nil
}
func (m *fakeMachine) Nameservers(context.Context) ([]dnsdelegation.Delegation, error) {
	m.w.add("nameservers on %s", m.ip)
	return []dnsdelegation.Delegation{{Domain: "cluster.example.org", Nameservers: []dnsdelegation.Nameserver{{Hostname: "ns1", IP: m.ip}}}}, nil
}
func (m *fakeMachine) OpenChain(context.Context) (string, func(), error) {
	return "http://127.0.0.1:1", func() {}, nil
}
func (m *fakeMachine) WaitNode(context.Context, time.Duration) error {
	m.w.add("wait %s", m.ip)
	return m.waitErr
}
func (m *fakeMachine) RestartNode(_ context.Context, _ time.Duration, force bool) error {
	m.w.add("restart-force=%v %s", force, m.ip)
	if m.quorumRefusal != "" && !force {
		return errors.New(m.quorumRefusal)
	}
	m.w.mu.Lock()
	m.w.restarting++
	if m.w.restarting > m.w.maxRestarting {
		m.w.maxRestarting = m.w.restarting
	}
	m.w.mu.Unlock()
	m.w.add("restart %s", m.ip)
	m.w.mu.Lock()
	m.w.restarting--
	m.w.mu.Unlock()
	return nil
}
func (m *fakeMachine) Close() { m.w.add("close %s", m.ip) }

type fakeEnroller struct {
	// quorumRefusal makes every machine's restart refuse without --force.
	quorumRefusal string
	w             *world
	facts         map[string]Facts
	fail          map[string]error
	created       map[string]*fakeMachine
	state         ChainState
}

func (e *fakeEnroller) Enroll(_ context.Context, req MachineRequest) (Machine, error) {
	e.w.add("enroll %s", req.IP)
	if err := e.fail[req.IP]; err != nil {
		return nil, err
	}
	facts, ok := e.facts[req.IP]
	if !ok {
		facts = freshFacts()
	}
	m := &fakeMachine{w: e.w, ip: req.IP, facts: facts, state: e.state, quorumRefusal: e.quorumRefusal}
	if e.created == nil {
		e.created = map[string]*fakeMachine{}
	}
	e.created[req.IP] = m
	return m, nil
}

func (e *fakeEnroller) Reach(ctx context.Context, ip, user string) (Machine, error) {
	e.w.add("reach %s", ip)
	return e.Enroll(ctx, MachineRequest{IP: ip, User: user})
}

// quorumBreak is the refusal of `orama node restart` on a cluster of voters
// voters (core/cmd/orama/internal/production/lifecycle/quorum.go), with the hint
// every refusal ends with.
func quorumBreak(voters int) string {
	return fmt.Sprintf("Stopping this node (Follower, voter) would break RQLite quorum: %d of %d configured voters would remain reachable, need %d.\n"+
		"  Use 'orama node restart --force' to proceed anyway.", voters-1, voters, voters/2+1)
}

const quorumUnreadable = "Cannot verify quorum safety: this node is a Follower VOTER but the cluster member list could not be read (timeout).\n" +
	"  Use 'orama node restart --force' to proceed anyway."

type fakeNetworks struct {
	w          *world
	faucet     bool
	genesisErr error
}

func (n fakeNetworks) Resolve(context.Context, string) (*netregistry.Network, error) {
	return &netregistry.Network{Manifest: &netregistry.Manifest{
		Name: "stagenet", ChainID: testChainID, Seeds: []string{"seed1.stagenet.example", "seed2.stagenet.example"},
		Channel: "nightly", MinVersion: "0.3.0", ReleaseRepo: "https://releases.example", ReleaseRootSHA256: testRootSHA, Faucet: n.faucet,
	}}, nil
}
func (n fakeNetworks) Genesis(context.Context, *netregistry.Network) ([]byte, error) {
	return []byte(`{"chain_id":"` + testChainID + `"}`), n.genesisErr
}

type fakeReleases struct{ w *world }

func (f fakeReleases) Fetch(_ context.Context, _ *netregistry.Network, arch string) (*Release, error) {
	f.w.add("fetch release %s", arch)
	return &Release{Version: "0.3.1", Arch: arch, ManifestSHA256: testManifest, CLISHA256: testCLISHA}, nil
}

type fakeTrust struct{ w *world }

func (f fakeTrust) TrustPoint(context.Context, *netregistry.Network) (*statesync.TrustPoint, error) {
	f.w.add("trust point")
	return &statesync.TrustPoint{
		Height: 4900, Hash: testSeedHash,
		Servers: []string{"https://seed1.stagenet.example/v1/chain/light", "https://seed2.stagenet.example/v1/chain/light"},
		Peers:   []statesync.Peer{{NodeID: strings.Repeat("a", 40), Host: "seed1.stagenet.example"}},
	}, nil
}

type fakeWallet struct{ err error }

func (f fakeWallet) Unlocked(context.Context) error               { return f.err }
func (f fakeWallet) EVMAddress(context.Context) (string, error)   { return testEVM, nil }
func (f fakeWallet) OramaAddress(context.Context) (string, error) { return testOperator, nil }

// fakeChain is the chain a ChainSession reads and writes.
type fakeChain struct {
	w      *world
	params ChainParams
}

func defaultParams() ChainParams {
	return ChainParams{
		MinBond: map[int]*big.Int{
			clusterreg.RoleStorage: big.NewInt(noramaPerOrama), clusterreg.RoleRelay: big.NewInt(noramaPerOrama),
			clusterreg.RoleExit: big.NewInt(noramaPerOrama),
		},
		BondPerGiB: big.NewInt(noramaPerOrama),
	}
}

func (c *fakeChain) Open(_ context.Context, m Machine, _ string) (ChainSession, error) {
	c.w.add("open chain on %s", m.Host())
	return &fakeSession{w: c.w, params: c.params}, nil
}

type fakeSession struct {
	w      *world
	params ChainParams
}

func (s *fakeSession) Close()                                      {}
func (s *fakeSession) Params(context.Context) (ChainParams, error) { return s.params, nil }
func (s *fakeSession) Balance(context.Context, string) (*big.Int, error) {
	s.w.mu.Lock()
	defer s.w.mu.Unlock()
	return new(big.Int).Set(s.w.balance), nil
}
func (s *fakeSession) OperatorRegistered(context.Context, string) (bool, error) {
	s.w.mu.Lock()
	defer s.w.mu.Unlock()
	return s.w.operatorRegistered, nil
}
func (s *fakeSession) Node(_ context.Context, id string) (*RegisteredNode, error) {
	s.w.mu.Lock()
	defer s.w.mu.Unlock()
	return s.w.nodes[id], nil
}
func (s *fakeSession) ValidatorExists(context.Context, string) (bool, error) {
	s.w.mu.Lock()
	defer s.w.mu.Unlock()
	return s.w.validator, nil
}
func (s *fakeSession) RegisterOperator(context.Context) (*onchain.Receipt, error) {
	s.w.add("tx register-operator")
	s.w.mu.Lock()
	s.w.operatorRegistered = true
	s.w.mu.Unlock()
	return &onchain.Receipt{}, nil
}
func (s *fakeSession) RegisterNode(_ context.Context, n clusterreg.NodeRegistration) (*onchain.Receipt, error) {
	s.w.add("tx register-node %s roles=%v asn=%d", n.NodeID, n.Roles, n.ASN)
	s.w.mu.Lock()
	s.w.nodes[n.NodeID] = &RegisteredNode{Roles: n.Roles, Bonds: map[int]*big.Int{}}
	s.w.mu.Unlock()
	return &onchain.Receipt{}, nil
}
func (s *fakeSession) Bond(_ context.Context, b clusterreg.Bond) (*onchain.Receipt, error) {
	s.w.add("tx bond %s role=%d amount=%s", b.NodeID, b.Role, b.Amount)
	amount, _ := new(big.Int).SetString(b.Amount, 10)
	s.w.mu.Lock()
	node := s.w.nodes[b.NodeID]
	if node.Bonds[b.Role] == nil {
		node.Bonds[b.Role] = new(big.Int)
	}
	node.Bonds[b.Role].Add(node.Bonds[b.Role], amount)
	s.w.mu.Unlock()
	return &onchain.Receipt{}, nil
}
func (s *fakeSession) DeclareCapacity(_ context.Context, c clusterreg.Capacity) (*onchain.Receipt, error) {
	s.w.add("tx capacity %s %d", c.NodeID, c.Bytes)
	s.w.mu.Lock()
	s.w.nodes[c.NodeID].CapacityBytes = c.Bytes
	s.w.mu.Unlock()
	return &onchain.Receipt{}, nil
}
func (s *fakeSession) CreateValidator(_ context.Context, spec onchain.ValidatorSpec) (*onchain.Receipt, error) {
	s.w.add("tx create-validator %s", spec.Moniker)
	s.w.mu.Lock()
	s.w.validator = true
	s.w.mu.Unlock()
	return &onchain.Receipt{}, nil
}

type fakeFunder struct{ w *world }

func (f fakeFunder) Fund(_ context.Context, _ *netregistry.Manifest, address string, amount *big.Int) error {
	f.w.add("faucet %s %s", address, amount)
	f.w.mu.Lock()
	defer f.w.mu.Unlock()
	f.w.faucetCalls++
	if f.w.faucetPays == nil {
		return errors.New("the faucet refused: cooldown")
	}
	f.w.balance.Add(f.w.balance, f.w.faucetPays)
	return nil
}

type fakeNames struct {
	w   *world
	err error
}

func (f fakeNames) Claim(_ context.Context, name, nodeID string) error {
	f.w.add("claim %s %s", name, nodeID)
	return f.err
}

type fakeRecorder struct {
	w       *world
	hosts   map[string][]RecordedNode
	active  string
	cluster map[string][]RecordedNode
	gateway map[string]string
	oper    map[string]string
}

func newRecorder(w *world) *fakeRecorder {
	return &fakeRecorder{w: w, hosts: map[string][]RecordedNode{}, cluster: map[string][]RecordedNode{}, gateway: map[string]string{}, oper: map[string]string{}}
}

func (r *fakeRecorder) Cluster(env, network, gateway string, nodes []RecordedNode) error {
	r.w.add("record cluster %s gateway=%q nodes=%d", env, gateway, len(nodes))
	r.cluster[env], r.gateway[env] = nodes, gateway
	return nil
}
func (r *fakeRecorder) Operator(env, operator string) error {
	r.w.add("record operator %s %s", env, operator)
	r.oper[env] = operator
	return nil
}
func (r *fakeRecorder) Hosts(env string) []RecordedNode { return r.hosts[env] }
func (r *fakeRecorder) ActiveFor(string) string         { return r.active }

type fakeDomain struct {
	w       *world
	waitErr error
}

func (f fakeDomain) Records(_ context.Context, via Machine, _ string) ([]string, error) {
	f.w.add("domain records via %s", via.Host())
	return []string{"cluster.example.org.\tIN\tNS\tns1.cluster.example.org.", "ns1.cluster.example.org.\tIN\tA\t203.0.113.10"}, nil
}
func (f fakeDomain) Wait(_ context.Context, via Machine, _ string, _, _ time.Duration) error {
	f.w.add("domain wait via %s", via.Host())
	return f.waitErr
}

// bufReporter collects events and lines.
type bufReporter struct {
	mu     sync.Mutex
	events []Event
	lines  []string
}

func (b *bufReporter) Emit(e Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, e)
}
func (b *bufReporter) Linef(format string, args ...any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lines = append(b.lines, fmt.Sprintf(format, args...))
}
func (b *bufReporter) text() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.Join(b.lines, "\n")
}
func (b *bufReporter) has(ip string, step Step, state State) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, e := range b.events {
		if e.Node == ip && e.Step == step && e.State == state {
			return true
		}
	}
	return false
}

// harness bundles everything a Run test needs.
type harness struct {
	w        *world
	enroll   *fakeEnroller
	chain    *fakeChain
	rec      *fakeRecorder
	report   *bufReporter
	deps     Deps
	networks fakeNetworks
}

func newHarness() *harness {
	w := newWorld()
	// Enough for a three-node full run: bonds, a self-bond and the reserve.
	w.balance = new(big.Int).Mul(big.NewInt(100_000), big.NewInt(noramaPerOrama))
	h := &harness{w: w, enroll: &fakeEnroller{w: w, state: ChainState{Running: true, Height: 5000}}, chain: &fakeChain{w: w, params: defaultParams()},
		rec: newRecorder(w), report: &bufReporter{}, networks: fakeNetworks{w: w}}
	h.deps = Deps{
		Networks: h.networks, Releases: fakeReleases{w}, Trust: fakeTrust{w}, Wallet: fakeWallet{}, Enroll: h.enroll, Chain: h.chain,
		ASN: func(context.Context, string) (uint32, error) { return 24940, nil }, Record: h.rec, Report: h.report,
		Timing: Timing{SyncPoll: time.Millisecond, SyncDeadline: time.Second, RestartBudget: time.Second, ReadyBudget: time.Second, DNSPoll: time.Millisecond, DNSDeadline: time.Second},
	}
	return h
}

func (h *harness) opts(ips ...string) Options {
	return Options{Network: "stagenet", IPs: ips, Name: "alice", Yes: true, TorNetwork: "", StorageGB: 10}
}
