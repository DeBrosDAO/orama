package setup

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/clusterreg"
	"github.com/DeBrosOfficial/network/pkg/netregistry"
)

// seatFake is a machine that can be a seat: the join's fake machine, with a chain
// home that starts without a genesis.
type seatFake struct {
	*fakeMachine
	idx int

	mu      sync.Mutex
	home    HomeState
	genesis []byte
	// built is the steps of the genesis this machine built, nil if it built none.
	built [][]string
	wired string

	initErr, buildErr error
	// tamper, when set, edits the genesis this machine builds (a machine that lies).
	tamper     func(map[string]any)
	health     ChainHealth
	epochs     []uint64
	epochCalls int
}

func fakeSeat(idx int) Seat {
	pub := make([]byte, 33)
	pub[0], pub[1] = 0x02, byte(idx)
	addr, err := clusterreg.AccountAddressOf(pub)
	if err != nil {
		panic(err)
	}
	key := make([]byte, 32)
	key[0], key[1] = 0xe0, byte(idx)
	return Seat{Address: addr, ConsensusPubKey: base64.StdEncoding.EncodeToString(key), NodeID: strings.Repeat(fmt.Sprintf("%x", idx+1), 40)[:40]}
}

func (s *seatFake) InitChain(_ context.Context, in InitChainInput) error {
	s.w.add("init-chain %s %s", s.ip, in.Node.Name)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.genesis = placeholderGenesis(in.ChainID)
	s.home = HomeState{Genesis: true, SHA256: netregistry.Digest(s.genesis)}
	return s.initErr
}

func (s *seatFake) Seat(context.Context) (Seat, error) {
	s.w.add("seat %s", s.ip)
	return fakeSeat(s.idx), nil
}

func (s *seatFake) HomeState(context.Context) (HomeState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.home, nil
}

func (s *seatFake) BuildGenesis(_ context.Context, steps [][]string) ([]byte, error) {
	s.w.add("build-genesis %s", s.ip)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.built = steps
	g := genesisFor{chainID: steps[0][3]}
	for _, st := range steps {
		switch {
		case len(st) > 1 && st[1] == "set-emission-params":
			g.allowStake, g.faucet = slices.Contains(st, "--allow-bootstrap-stake"), slices.Contains(st, "--faucet-enabled")
		case len(st) > 2 && st[1] == "add-bootstrap-validator":
			g.seats = append(g.seats, Seat{Address: st[2], Moniker: st[4], ConsensusPubKey: st[6]})
		}
	}
	doc := g.json()
	if s.tamper != nil {
		var m map[string]any
		_ = json.Unmarshal(doc, &m)
		s.tamper(m)
		doc, _ = json.Marshal(m)
	}
	return doc, s.buildErr
}

func (s *seatFake) ReadGenesis(context.Context) ([]byte, error) {
	s.w.add("read-genesis %s", s.ip)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.genesis, nil
}

func (s *seatFake) PutGenesis(_ context.Context, genesis []byte) error {
	s.w.add("put-genesis %s", s.ip)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.genesis = genesis
	s.home = HomeState{Genesis: true, Final: bytes.Contains(genesis, []byte("consensus_pubkey")), SHA256: netregistry.Digest(genesis), Started: s.home.Started}
	return nil
}

func (s *seatFake) WireChain(_ context.Context, in WireInput) error {
	s.w.add("wire %s", s.ip)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.wired = in.Peers
	return nil
}

func (s *seatFake) ChainHealth(context.Context) (ChainHealth, error) {
	s.w.add("health %s", s.ip)
	return s.health, nil
}

func (s *seatFake) Epoch(context.Context) (uint64, error) {
	s.w.add("epoch %s", s.ip)
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.epochs) == 0 {
		return createMinEpoch, nil
	}
	i := min(s.epochCalls, len(s.epochs)-1)
	s.epochCalls++
	return s.epochs[i], nil
}

// seatEnroller enrols seatFakes.
type seatEnroller struct {
	*fakeEnroller
	// homes seeds a machine's chain home and its genesis.
	homes   map[string]HomeState
	genesis map[string][]byte
	seats   map[string]*seatFake
	order   []string
}

func (e *seatEnroller) Enroll(ctx context.Context, req MachineRequest) (Machine, error) {
	m, err := e.fakeEnroller.Enroll(ctx, req)
	if err != nil {
		return nil, err
	}
	s := &seatFake{fakeMachine: m.(*fakeMachine), idx: len(e.order), home: e.homes[req.IP], genesis: e.genesis[req.IP],
		health: ChainHealth{Running: true, RPCUp: true, Height: 10}}
	e.seats[req.IP] = s
	e.order = append(e.order, req.IP)
	return s, nil
}

// createHarness is a harness whose machines can be seats, with the files a
// creation reads and writes in a temporary directory.
type createHarness struct {
	*harness
	seats      *seatEnroller
	publishDir string
	rootFile   string
}

func newCreateHarness(t *testing.T) *createHarness {
	t.Helper()
	h := newHarness()
	se := &seatEnroller{fakeEnroller: h.enroll, homes: map[string]HomeState{}, genesis: map[string][]byte{}, seats: map[string]*seatFake{}}
	h.deps.Enroll = se
	dir := t.TempDir()
	root := filepath.Join(dir, "release-root.json")
	if err := os.WriteFile(root, []byte(`{"signed":{"_type":"root"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return &createHarness{harness: h, seats: se, publishDir: filepath.Join(dir, "networks"), rootFile: root}
}

// createOpts is a creation of the network "stagenet" on the given machines.
func (h *createHarness) createOpts(ips ...string) Options {
	return Options{IPs: ips, Yes: true, StorageGB: 10, Create: &CreateOptions{
		Name: "stagenet", ChainID: "orama-stagenet-6", ReleaseRoot: h.rootFile, PublishDir: h.publishDir,
	}}
}

func (h *createHarness) mustCreate(t *testing.T, opts Options) *Result {
	t.Helper()
	res, err := Run(context.Background(), opts, h.deps)
	if err != nil {
		t.Fatalf("Run: %v\nlog:\n%s", err, strings.Join(h.w.entries(), "\n"))
	}
	return res
}

// genesisFor is the facts of a genesis as oramad writes them, which is what
// VerifyGenesis reads.
type genesisFor struct {
	chainID            string
	seats              []Seat
	allowStake, faucet bool
	// extra is added to app_state.bank.balances.
	extra []string
}

func (g genesisFor) json() []byte {
	members := make([]map[string]string, len(g.seats))
	for i, s := range g.seats {
		members[i] = map[string]string{"operator_address": s.Address, "moniker": s.Moniker, "consensus_pubkey": s.ConsensusPubKey}
	}
	balances := []map[string]string{}
	for _, a := range g.extra {
		balances = append(balances, map[string]string{"address": a})
	}
	doc, err := json.Marshal(map[string]any{
		"chain_id": g.chainID,
		"app_state": map[string]any{
			"power":    map[string]any{"params": map[string]string{"min_committee_size": strconv.Itoa(len(g.seats))}, "bootstrap_committee": members, "lambda": "0.000000000000000000", "exported": false},
			"emission": map[string]any{"params": map[string]any{"allow_bootstrap_stake": g.allowStake, "faucet_enabled": g.faucet}},
			"bank":     map[string]any{"balances": balances, "supply": []string{}},
			"genutil":  map[string]any{"gen_txs": []string{}},
		},
	})
	if err != nil {
		panic(err)
	}
	return doc
}

// fakeSeats are the seats of the first n machines, named as the run names them.
func fakeSeats(n int) []Seat {
	seats := make([]Seat, n)
	for i := range seats {
		seats[i] = fakeSeat(i)
		seats[i].Moniker = NodeNames(DefaultCreateNodeName, n)[i]
	}
	return seats
}

// finalGenesis is the genesis of the stagenet with the fake seats of machines idxs,
// as a run leaves it on a machine (consensus parameters set).
func finalGenesis(idxs ...int) []byte {
	var seats []Seat
	for _, i := range idxs {
		seat := fakeSeat(i)
		seat.Moniker = NodeNames(DefaultCreateNodeName, i+1)[i]
		seats = append(seats, seat)
	}
	out, err := ApplyConsensusParams(genesisFor{chainID: "orama-stagenet-6", seats: seats, allowStake: true, faucet: true}.json())
	if err != nil {
		panic(err)
	}
	return out
}

// carries gives a machine a chain home with the genesis in it.
func (e *seatEnroller) carries(ip string, genesis []byte, started bool) {
	e.genesis[ip] = genesis
	e.homes[ip] = HomeState{Genesis: true, Final: bytes.Contains(genesis, []byte("consensus_pubkey")), SHA256: netregistry.Digest(genesis), Started: started}
}

var errBoom = errors.New("boom")

func bigOramaMany() *big.Int { return big.NewInt(1_000_000 * noramaPerOrama) }
