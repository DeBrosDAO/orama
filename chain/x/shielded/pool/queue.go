package pool

import "cosmossdk.io/math"

// Request is one queued bond or deposit unshield.
type Request struct {
	Address string
	Amount  math.Int
}

// Grant is what the window actually pays that address.
type Grant struct {
	Address string
	Amount  math.Int
}

// Serve pays queued unshields pro rata by amount, then caps each address.
// Leftover capacity after the per-address cap is not given back to a larger
// request. A whale cannot sit at the head of the queue and take it.
func Serve(capacity, perAddress math.Int, reqs []Request) []Grant {
	grants := make([]Grant, len(reqs))
	if !capacity.IsPositive() || !perAddress.IsPositive() {
		for i, r := range reqs {
			grants[i] = Grant{Address: r.Address, Amount: math.ZeroInt()}
		}
		return grants
	}
	total := math.ZeroInt()
	for _, r := range reqs {
		if r.Amount.IsPositive() {
			total = total.Add(r.Amount)
		}
	}
	if !total.IsPositive() {
		for i, r := range reqs {
			grants[i] = Grant{Address: r.Address, Amount: math.ZeroInt()}
		}
		return grants
	}
	spent := math.ZeroInt()
	for i, r := range reqs {
		share := math.ZeroInt()
		if r.Amount.IsPositive() {
			share = capacity.Mul(r.Amount).Quo(total)
			if share.GT(r.Amount) {
				share = r.Amount
			}
			if share.GT(perAddress) {
				share = perAddress
			}
			remain := capacity.Sub(spent)
			if share.GT(remain) {
				share = remain
			}
		}
		grants[i] = Grant{Address: r.Address, Amount: share}
		spent = spent.Add(share)
	}
	return grants
}
