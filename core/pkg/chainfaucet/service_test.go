package chainfaucet

import (
	"context"
	"errors"
	"fmt"
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

// A drip that was broadcast and is not in a block by the deadline may still land, so it is pending
// and not a fault: the requester is told to look at the balance, and the allowance stays charged.
func TestDrip_aDripThatIsNotInABlockByTheDeadlineIsPending(t *testing.T) {
	for name, waitErr := range map[string]error{
		"the deadline":            fmt.Errorf("transaction ABCD: %w", clusterreg.ErrNotIncluded),
		"a lookup that failed":    errors.New("read the transaction result: connection reset"),
		"the run being cancelled": context.Canceled,
	} {
		t.Run(name, func(t *testing.T) {
			chain := &fakeChain{t: t, waitErr: waitErr}
			svc, _ := newTestService(t, chain, fakeIDs{id: testChainID})

			_, err := svc.Drip(context.Background(), recipientN(t, 2), norama(9))

			requireRefusal(t, err, KindPending)
		})
	}
}

// What a node says in an error after the transaction went out is not read as a refusal: a drip that
// may land is pending, whatever words the error carries.
func TestDrip_textInAnErrorAfterTheBroadcastIsNotReadAsARefusal(t *testing.T) {
	chain := &fakeChain{t: t, waitErr: errors.New("read the transaction result: faucet recipient is still within its cooldown")}
	svc, _ := newTestService(t, chain, fakeIDs{id: testChainID})

	_, err := svc.Drip(context.Background(), recipientN(t, 2), norama(9))

	requireRefusal(t, err, KindPending)
}

// A broadcast whose answer is lost may have been taken by the node: it is pending, and a node that
// refused it is a fault the allowance goes back for.
func TestDrip_aBroadcastWhoseAnswerIsLostIsPending(t *testing.T) {
	for name, tc := range map[string]struct {
		broadcast error
		pending   bool
	}{
		"a timeout":        {context.DeadlineExceeded, true},
		"a server error":   {&clusterreg.StatusError{Code: 502}, true},
		"unreadable":       {errors.New("broadcast response is not JSON"), true},
		"a client error":   {&clusterreg.StatusError{Code: 400}, false},
		"refused by check": {fmt.Errorf("%w (code 5): unauthorized", clusterreg.ErrBroadcastRejected), false},
	} {
		t.Run(name, func(t *testing.T) {
			chain := &fakeChain{t: t, broadcast: tc.broadcast}
			svc, _ := newTestService(t, chain, fakeIDs{id: testChainID})

			_, err := svc.Drip(context.Background(), recipientN(t, 2), norama(9))

			if tc.pending {
				requireRefusal(t, err, KindPending)
			} else if !errors.Is(err, ErrFault) {
				t.Fatalf("err = %v, want ErrFault", err)
			}
		})
	}
}

// A transaction that is in a block and failed there paid its fee and minted nothing: it is a fault,
// and the allowance goes back, unlike a drip that may still land.
func TestDrip_aDripThatFailedInItsBlockIsNotPending(t *testing.T) {
	chain := &fakeChain{t: t, waitErr: fmt.Errorf("%w in block 7 (code 9): out of gas", clusterreg.ErrTxFailed)}
	svc, _ := newTestService(t, chain, fakeIDs{id: testChainID})

	_, err := svc.Drip(context.Background(), recipientN(t, 2), norama(9))

	if !errors.Is(err, ErrFault) {
		t.Fatalf("err = %v, want ErrFault", err)
	}
}

// A chain that refuses the drip when its block runs it (the cooldown raced) is a refusal the
// requester can act on, though the transaction was sent.
func TestDrip_aRefusalInTheBlockIsTypedNotPending(t *testing.T) {
	chain := &fakeChain{t: t, waitErr: fmt.Errorf("%w in block 7 (code 5): faucet recipient is still within its cooldown: soon", clusterreg.ErrTxFailed)}
	svc, _ := newTestService(t, chain, fakeIDs{id: testChainID})

	_, err := svc.Drip(context.Background(), recipientN(t, 2), norama(9))

	requireRefusal(t, err, KindCooldown)
}

func TestDrip_aFaultIsHiddenFromTheRequesterAndNotAKnownRefusal(t *testing.T) {
	for name, chain := range map[string]*fakeChain{
		"the chain cannot be reached": {simErr: errors.New("dial tcp 198.18.0.2:31003: connect: connection refused")},
		"the block failed":            {waitErr: fmt.Errorf("%w in block 7 (code 9): out of gas", clusterreg.ErrTxFailed)},
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
	var to []string
	for i := 0; i < n; i++ {
		to = append(to, recipientN(t, byte(10+i)))
	}
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.Drip(context.Background(), to[i], norama(9))
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
	// The addresses are made here: a test fails from its own goroutine only.
	var to []string
	for i := 0; i < QueueDepth+2; i++ {
		to = append(to, recipientN(t, byte(20+i)))
	}
	results := make(chan error, QueueDepth+1)
	ask := func(recipient string) {
		go func() {
			_, err := svc.Drip(context.Background(), recipient, norama(9))
			results <- err
		}()
	}
	// One drip in flight with the worker, then exactly QueueDepth waiting for their turn.
	ask(to[0])
	waitFor(t, func() bool { chain.mu.Lock(); defer chain.mu.Unlock(); return len(chain.hashes) == 1 })
	for i := 1; i <= QueueDepth; i++ {
		ask(to[i])
		waitFor(t, func() bool { return len(svc.queue) == i })
	}

	_, err := svc.Drip(context.Background(), to[QueueDepth+1], norama(9))

	requireRefusal(t, err, KindBusy)
	close(chain.hold)
	for i := 0; i <= QueueDepth; i++ {
		if err := <-results; err != nil {
			t.Errorf("a queued drip failed: %v", err)
		}
	}
}

// A requester who gives up while its drip is still queued is told nothing was sent, and nothing is:
// "pending" would have it wait for a drip that never comes, and keep its allowance charged for it.
func TestDrip_aRequesterWhoLeavesWhileQueuedIsToldNothingWasSent(t *testing.T) {
	chain := &fakeChain{t: t, hold: make(chan struct{})}
	svc, _ := newTestService(t, chain, fakeIDs{id: testChainID})
	to := []string{recipientN(t, 2), recipientN(t, 3), recipientN(t, 4)}
	first := make(chan error, 1)
	go func() {
		_, err := svc.Drip(context.Background(), to[0], norama(9))
		first <- err
	}()
	waitFor(t, func() bool { chain.mu.Lock(); defer chain.mu.Unlock(); return len(chain.hashes) == 1 })

	ctx, cancel := context.WithCancel(context.Background())
	second := make(chan error, 1)
	go func() {
		_, err := svc.Drip(ctx, to[1], norama(9))
		second <- err
	}()
	waitFor(t, func() bool { return len(svc.queue) == 1 })
	cancel()
	requireRefusal(t, <-second, KindBusy)

	close(chain.hold)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	// Drips are taken in order, so once a third has been made the abandoned one has been passed.
	if _, err := svc.Drip(context.Background(), to[2], norama(9)); err != nil {
		t.Fatal(err)
	}
	chain.mu.Lock()
	defer chain.mu.Unlock()
	if len(chain.hashes) != 2 {
		t.Errorf("%d transactions were sent; the first and the third, not the abandoned one", len(chain.hashes))
	}
}

// A drip that is on its way when its requester gives up is reported pending, never as not made.
func TestDrip_aRequesterWhoLeavesWhileTheDripIsInFlightIsToldItIsPending(t *testing.T) {
	chain := &fakeChain{t: t, hold: make(chan struct{})}
	svc, _ := newTestService(t, chain, fakeIDs{id: testChainID})
	ctx, cancel := context.WithCancel(context.Background())
	res := make(chan error, 1)
	to := recipientN(t, 2)
	go func() {
		_, err := svc.Drip(ctx, to, norama(9))
		res <- err
	}()
	waitFor(t, func() bool { chain.mu.Lock(); defer chain.mu.Unlock(); return len(chain.hashes) == 1 })
	cancel()
	requireRefusal(t, <-res, KindPending)
	close(chain.hold)
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
	if !errors.Is(err, ErrFault) {
		t.Fatalf("a stopped faucet = %v, want ErrFault", err)
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

// The live stagenet faucet answered an anonymous caller with "faucet recipient is still within its
// cooldown [DeBrosOfficial/network/chain/x/emission/keeper/msg_server.go:98] with gas used: '94339'":
// the SDK's trailer names a source file and the gas the node spent. The reason ends before it.
func TestDrip_theSDKsSourceLocationAndGasAreNotPartOfTheReason(t *testing.T) {
	chain := &fakeChain{t: t, simErr: errors.New("the chain refused the transaction in simulation: failed to execute message; message index: 0: faucet recipient is still within its cooldown [DeBrosOfficial/network/chain/x/emission/keeper/msg_server.go:98] with gas used: '94339' (chain API returned HTTP 500)")}
	svc, _ := newTestService(t, chain, fakeIDs{id: testChainID})

	_, err := svc.Drip(context.Background(), recipientN(t, 2), norama(9))

	r := requireRefusal(t, err, KindCooldown)
	if want := "faucet recipient is still within its cooldown"; r.Message != want {
		t.Errorf("message = %q, want %q", r.Message, want)
	}
}

// A node's error joined from several lines carries the chain's reason first; the lines after it
// are the node's and are not shown to an anonymous caller.
func TestDrip_linesJoinedAfterTheReasonAreNotPartOfIt(t *testing.T) {
	chain := &fakeChain{t: t, simErr: errors.New("faucet recipient is still within its cooldown\ngoroutine 7 [running]:\nmain.go:12")}
	svc, _ := newTestService(t, chain, fakeIDs{id: testChainID})

	_, err := svc.Drip(context.Background(), recipientN(t, 2), norama(9))

	r := requireRefusal(t, err, KindCooldown)
	if want := "faucet recipient is still within its cooldown"; r.Message != want {
		t.Errorf("message = %q, want %q", r.Message, want)
	}
}
