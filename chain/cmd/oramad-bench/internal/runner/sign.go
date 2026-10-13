package runner

import (
	"context"
	"fmt"

	"cosmossdk.io/math"

	"github.com/DeBrosOfficial/network/chain/client/tx"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/account"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/broadcast"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/gen"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/ops"
)

// signer signs a type's transactions offline, with sequences decided before anything is sent.
type signer struct {
	chain    *ops.Chain
	accounts []tx.Account
	numbers  map[string]uint64
	seq      *account.Sequencer
}

func newSigner(chain *ops.Chain, accounts []tx.Account) *signer {
	return &signer{chain: chain, accounts: accounts, numbers: map[string]uint64{}, seq: account.NewSequencer()}
}

// reload reads every account's number and committed sequence from the chain. It is called before
// each step: a step that left a transaction unincluded would otherwise leave every later
// pre-signed sequence one too high.
func (s *signer) reload(ctx context.Context) error {
	addrs := make([]string, len(s.accounts))
	for i, a := range s.accounts {
		addrs[i] = a.Address
	}
	return s.seq.Reload(ctx, func(ctx context.Context, addr string) (uint64, error) {
		number, seq, err := s.chain.Client.Account(ctx, addr)
		if err != nil {
			return 0, err
		}
		s.numbers[addr] = number
		return seq, nil
	}, addrs)
}

// presign plans and signs transactions from to from+n-1 of a type. gas is the limit signed
// transactions declare; price is the gas price they offer.
func (s *signer) presign(g gen.Generator, from, n int, gas uint64, price math.Int) ([]broadcast.Job, error) {
	jobs := make([]broadcast.Job, 0, n)
	for k := from; k < from+n; k++ {
		p, err := g.Plan(k)
		if err != nil {
			return nil, fmt.Errorf("plan transaction %d of %s: %w", k, g.Name(), err)
		}
		job, err := s.sign(p, k, gas, price)
		if err != nil {
			return nil, fmt.Errorf("sign transaction %d of %s: %w", k, g.Name(), err)
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

func (s *signer) sign(p gen.Planned, k int, gas uint64, price math.Int) (broadcast.Job, error) {
	if p.Account == gen.Signerless {
		raw, err := s.chain.Signerless(p.Gas, p.Msg)
		if err != nil {
			return broadcast.Job{}, err
		}
		return broadcast.Job{Hash: txHash(raw), Raw: raw, Shard: k}, nil
	}
	if p.Account < 0 || p.Account >= len(s.accounts) {
		return broadcast.Job{}, fmt.Errorf("planned for account %d, there are %d", p.Account, len(s.accounts))
	}
	acct := s.accounts[p.Account]
	seq, err := s.seq.Take(acct.Address)
	if err != nil {
		return broadcast.Job{}, err
	}
	limit := gas
	if p.Gas != 0 {
		limit = p.Gas
	}
	raw, err := s.chain.Sign(acct, s.numbers[acct.Address], seq, limit, price, p.Msg)
	if err != nil {
		return broadcast.Job{}, err
	}
	return broadcast.Job{Hash: txHash(raw), Raw: raw, Account: acct.Address, Seq: seq, Shard: p.Account}, nil
}
