package chainfaucet

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/clusterreg"
	"github.com/DeBrosOfficial/network/pkg/onchain"
)

func TestDrip_aDripIsSignedByTheFaucetAndReportsItsBlock(t *testing.T) {
	chain := &fakeChain{t: t}
	svc, key := newTestService(t, chain, fakeIDs{id: testChainID})
	to := recipientN(t, 2)

	got, err := svc.Drip(context.Background(), to, norama(5_000_000_000))

	if err != nil {
		t.Fatal(err)
	}
	if got.Height != 1001 || got.TxHash != chain.hashes[0] || got.Amount.Int64() != 5_000_000_000 {
		t.Errorf("dripped = %+v", got)
	}
	if svc.Address() != key.Address() {
		t.Error("the service funds from another account than its key")
	}
}

func TestDrip_aRefusalTheChainGivesInTheSimulationIsTypedAndSignsNothing(t *testing.T) {
	for name, tc := range map[string]struct {
		chainSays string
		kind      Kind
	}{
		"cooldown":     {"faucet recipient is still within its cooldown: orama1x drew at 1, next drip allowed at 86401 (now 5)", KindCooldown},
		"epoch cap":    {"faucet epoch cap exceeded: epoch 3 minted 1, drip 2, cap 3", KindEpochCap},
		"too much":     {"faucet amount must be positive and at most faucet_max_drip: got 9, faucet_max_drip is 1000000000000", KindBadAmount},
		"module":       {"faucet recipient is invalid or a blocked address: orama1x is a module or blocked account", KindBadRecipient},
		"disabled":     {"the faucet is disabled (faucet_enabled is false)", KindDisabled},
		"production":   {"the faucet runs only on a devnet, stagenet or localnet chain-id: chain-id \"orama-1\"", KindDisabled},
		"cannot pay":   {"insufficient funds: spendable balance 0norama is smaller than 5norama", KindUnavailable},
		"fee too low":  {"insufficient fee; got: 1norama required: 50norama", KindUnavailable},
		"multi line":   {"failed to execute message; message index: 0:\nfaucet recipient is still within its cooldown: soon", KindCooldown},
		"with escapes": {"faucet epoch cap exceeded: \x1b[2Jforged", KindEpochCap},
	} {
		t.Run(name, func(t *testing.T) {
			chain := &fakeChain{t: t, simErr: errors.New("the chain refused the transaction in simulation: " + tc.chainSays + " (chain API returned HTTP 500)")}
			svc, _ := newTestService(t, chain, fakeIDs{id: testChainID})

			_, err := svc.Drip(context.Background(), recipientN(t, 2), norama(9))

			r := requireRefusal(t, err, tc.kind)
			if strings.ContainsAny(r.Message, "\n\x1b") || strings.Contains(r.Message, "chain API returned") {
				t.Errorf("message %q is not the chain's reason on one line", r.Message)
			}
			if len(chain.hashes) != 0 {
				t.Error("a refused drip was broadcast")
			}
		})
	}
}

func TestDrip_theChainsReasonIsQuotedFromItsOwnWords(t *testing.T) {
	chain := &fakeChain{t: t, simErr: errors.New("the chain refused the transaction in simulation: failed to execute message; message index: 0: faucet recipient is still within its cooldown: orama1x drew at 1, next drip allowed at 86401 (now 5) (chain API returned HTTP 500)")}
	svc, _ := newTestService(t, chain, fakeIDs{id: testChainID})

	_, err := svc.Drip(context.Background(), recipientN(t, 2), norama(9))

	r := requireRefusal(t, err, KindCooldown)
	if want := "faucet recipient is still within its cooldown: orama1x drew at 1, next drip allowed at 86401 (now 5)"; r.Message != want {
		t.Errorf("message = %q, want %q", r.Message, want)
	}
}

func TestDrip_aFaucetAccountThatDoesNotExistIsUnavailableAndSaysWhichToFund(t *testing.T) {
	chain := &fakeChain{t: t, accountErr: &clusterreg.StatusError{Code: 404}}
	svc, key := newTestService(t, chain, fakeIDs{id: testChainID})

	_, err := svc.Drip(context.Background(), recipientN(t, 2), norama(9))

	r := requireRefusal(t, err, KindUnavailable)
	mustContain(t, r.Message, key.Address(), "fund")
}

func TestDrip_refusesWhatNoChainStateCouldAccept(t *testing.T) {
	chain := &fakeChain{t: t}
	svc, key := newTestService(t, chain, fakeIDs{id: testChainID})
	good := recipientN(t, 2)
	for name, tc := range map[string]struct {
		to     string
		amount int64
		kind   Kind
	}{
		"empty recipient":        {"", 1, KindBadRecipient},
		"not an address":         {"hello", 1, KindBadRecipient},
		"bad checksum":           {good[:len(good)-1] + "q", 1, KindBadRecipient},
		"uppercase":              {strings.ToUpper(good), 1, KindBadRecipient},
		"another prefix":         {"cosmos1" + strings.TrimPrefix(good, "orama1"), 1, KindBadRecipient},
		"the faucet itself":      {key.Address(), 1, KindBadRecipient},
		"a zero amount":          {good, 0, KindBadAmount},
		"a negative amount":      {good, -3, KindBadAmount},
		"an amount of 19 digits": {good, 0, KindBadAmount},
	} {
		t.Run(name, func(t *testing.T) {
			amount := norama(tc.amount)
			if name == "an amount of 19 digits" {
				amount, _ = new(big.Int).SetString(strings.Repeat("9", MaxAmountDigits+1), 10)
			}
			_, err := svc.Drip(context.Background(), tc.to, amount)
			requireRefusal(t, err, tc.kind)
		})
	}
	if _, err := svc.Drip(context.Background(), good, nil); err == nil {
		t.Error("a drip with no amount was accepted")
	}
	if len(chain.hashes) != 0 {
		t.Errorf("%d transactions reached the chain for requests that could not be accepted", len(chain.hashes))
	}
}

// A production chain is never signed for, whatever the genesis says.
func TestDrip_aProductionChainIsRefusedBeforeAnythingIsRead(t *testing.T) {
	chain := &fakeChain{t: t}
	svc, _ := newTestService(t, chain, fakeIDs{id: "orama-1"})

	_, err := svc.Drip(context.Background(), recipientN(t, 2), norama(9))

	r := requireRefusal(t, err, KindDisabled)
	mustContain(t, r.Message, "test network")
	if len(chain.hashes) != 0 {
		t.Error("the faucet signed on a production chain")
	}
}

// The chain id is read for every drip: a chain reset under a new id must not be signed for under
// the old one, and a reset into a production id must stop the faucet.
func TestDrip_theChainIDIsReadForEveryDrip(t *testing.T) {
	chain := &fakeChain{t: t}
	ids := &switchingIDs{id: testChainID}
	svc, _ := newTestService(t, chain, ids)
	if _, err := svc.Drip(context.Background(), recipientN(t, 2), norama(9)); err != nil {
		t.Fatal(err)
	}
	ids.set("orama-1")
	_, err := svc.Drip(context.Background(), recipientN(t, 3), norama(9))
	requireRefusal(t, err, KindDisabled)
}

type switchingIDs struct {
	mu sync.Mutex
	id string
}

func (s *switchingIDs) set(id string) { s.mu.Lock(); s.id = id; s.mu.Unlock() }
func (s *switchingIDs) ChainID(context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.id, nil
}

func TestDrip_aFaultIsHiddenFromTheRequesterAndNotAKnownRefusal(t *testing.T) {
	for name, chain := range map[string]*fakeChain{
		"the chain cannot be reached": {simErr: errors.New("dial tcp 198.18.0.2:31003: connect: connection refused")},
		"the block never comes":       {waitErr: clusterreg.ErrNotIncluded},
		"the block failed":            {waitErr: errors.New("the transaction failed in block 7 (code 9): out of gas")},
	} {
		t.Run(name, func(t *testing.T) {
			chain.t = t
			svc, _ := newTestService(t, chain, fakeIDs{id: testChainID})

			_, err := svc.Drip(context.Background(), recipientN(t, 2), norama(9))

			if !errors.Is(err, ErrFault) {
				t.Fatalf("err = %v, want ErrFault", err)
			}
			if strings.Contains(err.Error(), "198.18") || strings.Contains(err.Error(), "block 7") {
				t.Errorf("the requester is told the chain's details: %v", err)
			}
		})
	}
	svc, _ := newTestService(t, &fakeChain{t: t}, fakeIDs{err: errors.New("node_info: connection refused")})
	if _, err := svc.Drip(context.Background(), recipientN(t, 2), norama(9)); !errors.Is(err, ErrFault) {
		t.Errorf("an unreadable chain id = %v, want ErrFault", err)
	}
}

// Many requests at once: the faucet account signs one transaction at a time, each with the
// sequence the previous block left, never two together.
func TestDrip_concurrentDripsSignOneAtATimeWithConsecutiveSequences(t *testing.T) {
	chain := &fakeChain{t: t}
	svc, _ := newTestService(t, chain, fakeIDs{id: testChainID})
	const n = 12
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.Drip(context.Background(), recipientN(t, byte(10+i)), norama(9))
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("a drip failed: %v", err)
		}
	}
	chain.mu.Lock()
	defer chain.mu.Unlock()
	if chain.maxFlight != 1 {
		t.Errorf("%d transactions were in flight together; the faucet signs one at a time", chain.maxFlight)
	}
	for i, seq := range chain.signedSeqs {
		if seq != uint64(i) {
			t.Fatalf("transaction %d signed sequence %d, want %d: %v", i, seq, i, chain.signedSeqs)
		}
	}
}

func TestDrip_aFullQueueIsToldToComeBack(t *testing.T) {
	chain := &fakeChain{t: t, hold: make(chan struct{})}
	svc, _ := newTestService(t, chain, fakeIDs{id: testChainID})
	started := make(chan struct{}, QueueDepth+2)
	results := make(chan error, QueueDepth+2)
	for i := 0; i < QueueDepth+1; i++ {
		go func() {
			started <- struct{}{}
			_, err := svc.Drip(context.Background(), recipientN(t, byte(20+i)), norama(9))
			results <- err
		}()
	}
	for i := 0; i < QueueDepth+1; i++ {
		<-started
	}
	waitFor(t, func() bool { return len(svc.queue) == QueueDepth })
	_, err := svc.Drip(context.Background(), recipientN(t, 99), norama(9))
	requireRefusal(t, err, KindBusy)
	close(chain.hold)
	for i := 0; i < QueueDepth+1; i++ {
		if err := <-results; err != nil {
			t.Errorf("a queued drip failed: %v", err)
		}
	}
}

// A requester who leaves while its drip waits costs the faucet nothing: the drip is not sent.
func TestDrip_aRequesterWhoLeavesBeforeItsTurnIsNeverSent(t *testing.T) {
	chain := &fakeChain{t: t, hold: make(chan struct{})}
	svc, _ := newTestService(t, chain, fakeIDs{id: testChainID})
	first := make(chan error, 1)
	go func() {
		_, err := svc.Drip(context.Background(), recipientN(t, 2), norama(9))
		first <- err
	}()
	waitFor(t, func() bool { chain.mu.Lock(); defer chain.mu.Unlock(); return len(chain.hashes) == 1 })

	ctx, cancel := context.WithCancel(context.Background())
	second := make(chan error, 1)
	go func() {
		_, err := svc.Drip(ctx, recipientN(t, 3), norama(9))
		second <- err
	}()
	waitFor(t, func() bool { return len(svc.queue) == 1 })
	cancel()
	requireRefusal(t, <-second, KindPending)

	close(chain.hold)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	// Drips are taken in order, so once a third has been made the abandoned one has been passed.
	if _, err := svc.Drip(context.Background(), recipientN(t, 4), norama(9)); err != nil {
		t.Fatal(err)
	}
	chain.mu.Lock()
	defer chain.mu.Unlock()
	if len(chain.hashes) != 2 {
		t.Errorf("%d transactions were sent; the first and the third, not the abandoned one", len(chain.hashes))
	}
}

func TestDrip_aStoppedServiceAnswersAFault(t *testing.T) {
	key, _ := NewKey()
	ctx, cancel := context.WithCancel(context.Background())
	svc, err := New(ctx, key, &fakeChain{t: t}, fakeIDs{id: testChainID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	// The worker stops; a drip that is queued after that is answered when the service's context ends.
	_, err = svc.Drip(context.Background(), recipientN(t, 2), norama(9))
	if err == nil {
		t.Fatal("a stopped faucet made a drip")
	}
}

func TestNew_needsItsParts(t *testing.T) {
	key, _ := NewKey()
	for name, args := range map[string]struct {
		key   *Key
		chain onchain.Chain
		ids   ChainIDSource
	}{
		"no key":   {nil, &fakeChain{}, fakeIDs{}},
		"no chain": {key, nil, fakeIDs{}},
		"no ids":   {key, &fakeChain{}, nil},
	} {
		if _, err := New(context.Background(), args.key, args.chain, args.ids, nil); err == nil {
			t.Errorf("%s: New accepted it", name)
		}
	}
}

// waitFor polls cond until it holds, failing the test at the deadline.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("the condition did not hold before the deadline")
		}
		time.Sleep(time.Millisecond)
	}
}
