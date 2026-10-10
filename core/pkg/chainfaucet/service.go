package chainfaucet

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/DeBrosOfficial/network/pkg/clusterreg"
	"github.com/DeBrosOfficial/network/pkg/onchain"
)

const (
	// DefaultDripNorama is the drip a request that names no amount gets: 100 ORAMA, a tenth of the
	// chain's default maximum drip.
	DefaultDripNorama = 100_000_000_000
	// MaxAmountDigits bounds the amount of a request before it is parsed: no drip is anywhere
	// near 10^18 norama (a billion ORAMA).
	MaxAmountDigits = 18

	// QueueDepth is how many drips wait for the faucet's turn. The faucet account signs one
	// transaction at a time, and a request beyond the queue is told to come back (KindBusy)
	// instead of waiting without end.
	QueueDepth = 16
	// sendTimeout bounds one drip from the simulation to its block. It is the wait for inclusion
	// the onchain client makes (clusterreg.InclusionTimeout) with room for the reads before it:
	// the next drip must not start while this one can still land, or the two would sign the same
	// sequence number.
	sendTimeout = clusterreg.InclusionTimeout + 30*time.Second
)

// ErrFault wraps what went wrong when it was not a refusal: the chain could not be reached, or a
// node answered nonsense. The details are in the log, and none of them go to the requester.
var ErrFault = errors.New("the faucet could not make the drip")

// Dripped is a drip that is in a block.
type Dripped struct {
	TxHash string
	Height int64
	Amount *big.Int
}

// ChainIDSource says which chain the faucet's chain node runs.
type ChainIDSource interface {
	ChainID(ctx context.Context) (string, error)
}

// Service makes the drips of one faucet account, one at a time.
type Service struct {
	key    *Key
	chain  onchain.Chain
	ids    ChainIDSource
	log    *zap.Logger
	ctx    context.Context
	queue  chan *job
	sendTO time.Duration
}

type job struct {
	ctx       context.Context
	recipient string
	amount    *big.Int
	done      chan result
	// state is who gets the job: the worker when it takes it (jobRunning), or the requester when it
	// gives up first (jobAbandoned). Whichever changes it from jobQueued first decides, so a drip
	// is never both reported as not made and sent.
	state atomic.Int32
}

const (
	jobQueued int32 = iota
	jobRunning
	jobAbandoned
)

type result struct {
	dripped *Dripped
	err     error
}

// New starts the service: its worker runs until ctx ends. chain is the faucet's own chain node,
// the REST API a co-located gateway reaches; ids reads that node's chain id for every drip, so a
// chain that was reset under a new id is never signed for under the old one. log may be nil.
func New(ctx context.Context, key *Key, chain onchain.Chain, ids ChainIDSource, log *zap.Logger) (*Service, error) {
	if key == nil || chain == nil || ids == nil {
		return nil, errors.New("a faucet service needs a key, a chain and a chain id source")
	}
	if log == nil {
		log = zap.NewNop()
	}
	s := &Service{key: key, chain: chain, ids: ids, log: log, ctx: ctx, queue: make(chan *job, QueueDepth), sendTO: sendTimeout}
	go s.run()
	return s, nil
}

// Address is the faucet account: the account that must be funded.
func (s *Service) Address() string { return s.key.Address() }

// Drip mints amount norama to recipient. It returns a *Refusal for a drip that was refused, and
// ErrFault when it could not be made. A drip whose requester gives up while it waits is not made
// unless it was already sent.
func (s *Service) Drip(ctx context.Context, recipient string, amount *big.Int) (*Dripped, error) {
	if r := s.check(recipient, amount); r != nil {
		return nil, r
	}
	j := &job{ctx: ctx, recipient: recipient, amount: amount, done: make(chan result, 1)}
	select {
	case s.queue <- j:
	default:
		return nil, refuse(KindBusy, "%d drips are already waiting for the faucet; try again shortly", QueueDepth)
	}
	select {
	case res := <-j.done:
		return res.dripped, res.err
	case <-ctx.Done():
		if j.state.CompareAndSwap(jobQueued, jobAbandoned) || j.state.Load() == jobAbandoned {
			return nil, refuse(KindBusy, "the faucet did not reach your drip in time and sent nothing; try again shortly")
		}
		select {
		case res := <-j.done:
			// It finished as the requester gave up: the answer is the better one to give.
			return res.dripped, res.err
		default:
		}
		return nil, refuse(KindPending, "the drip was sent and is not in a block yet; look at the balance of %s shortly", recipient)
	case <-s.ctx.Done():
		return nil, fmt.Errorf("%w: the gateway is shutting down", ErrFault)
	}
}

// check refuses what no chain state could make acceptable.
func (s *Service) check(recipient string, amount *big.Int) *Refusal {
	if _, err := clusterreg.CanonicalAccount(recipient); err != nil {
		return refuse(KindBadRecipient, "the recipient is not a canonical orama address (orama1..., lowercase): %v", err)
	}
	if recipient == s.key.Address() {
		return refuse(KindBadRecipient, "the recipient is the faucet's own account")
	}
	if amount == nil || amount.Sign() <= 0 {
		return refuse(KindBadAmount, "the amount must be more than zero norama")
	}
	if len(amount.String()) > MaxAmountDigits {
		return refuse(KindBadAmount, "the amount has more than %d digits", MaxAmountDigits)
	}
	return nil
}

// run takes the drips in order. One at a time is the point: every transaction of the faucet account
// uses the sequence number the previous one leaves, which is known only once it is in a block.
func (s *Service) run() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case j := <-s.queue:
			s.process(j)
		}
	}
}

func (s *Service) process(j *job) {
	if j.ctx.Err() != nil {
		// The requester left while the drip waited: sending it would spend a fee for nobody.
		j.state.CompareAndSwap(jobQueued, jobAbandoned)
	}
	if !j.state.CompareAndSwap(jobQueued, jobRunning) {
		return
	}
	dripped, err := s.drip(j)
	if err != nil {
		var refusal *Refusal
		if !errors.As(err, &refusal) {
			s.log.Warn("faucet drip failed", zap.String("recipient", j.recipient), zap.Error(err))
			err = ErrFault
		}
	}
	j.done <- result{dripped: dripped, err: err}
}

func (s *Service) drip(j *job) (*Dripped, error) {
	ctx, cancel := context.WithTimeout(s.ctx, s.sendTO)
	defer cancel()
	chainID, err := s.ids.ChainID(ctx)
	if err != nil {
		return nil, fmt.Errorf("read the chain id: %w", err)
	}
	if !IsTestNetwork(chainID) {
		return nil, refuse(KindDisabled, "the faucet runs only on a test network (a chain id with one of %v), and this chain is not one", TestNetworkMarkers)
	}
	client, err := onchain.New(s.chain, s.key, chainID)
	if err != nil {
		return nil, err
	}
	receipt, err := client.Faucet(ctx, j.recipient, j.amount)
	if err != nil {
		if refusal := classify(err, s.key.Address()); refusal != nil {
			return nil, refusal
		}
		var sent *onchain.SentError
		if errors.As(err, &sent) && !errors.Is(err, clusterreg.ErrTxFailed) {
			// Broadcast, and then the wait or the lookup failed: it may still land.
			return nil, refuse(KindPending, "the drip was sent and is not in a block yet; look at the balance of %s shortly", j.recipient)
		}
		return nil, err
	}
	return &Dripped{TxHash: receipt.Hash, Height: receipt.Height, Amount: j.amount}, nil
}
