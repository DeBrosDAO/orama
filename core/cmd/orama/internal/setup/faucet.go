package setup

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/chainfaucet"
	"github.com/DeBrosOfficial/network/pkg/httputil"
	"github.com/DeBrosOfficial/network/pkg/netregistry"
	"github.com/DeBrosOfficial/network/pkg/statesync"
)

// faucetRequestTimeout bounds one seed's answer: the gateway answers when the drip is in a block,
// and waits for that up to 45 seconds (handlers/chainread/faucet.go, faucetWait).
const faucetRequestTimeout = 60 * time.Second

// gatewayFaucet funds an account from the public faucet the seeds of a test network serve
// (POST /v1/chain/faucet): the seeds are the network's own gateways, and a newcomer needs no node
// of it, no SSH access and no funds to ask. The seeds are replicas of one another, so they are
// asked in order and the first to pay ends the matter; a seed that serves no faucet, is busy or
// cannot pay is not an answer about the network, and the next is asked. An answer that no other
// seed could change (the recipient is in its cooldown, the drip is over the maximum, the epoch's
// cap is spent) ends the run at once. When no seed pays, every seed's answer is reported.
//
// `orama chain faucet` signs on a node of the network over SSH and stays for the operators of one.
type gatewayFaucet struct {
	client chainfaucet.Doer
}

func newGatewayFaucet() gatewayFaucet {
	client := statesync.NewHTTPClient()
	client.Timeout = faucetRequestTimeout
	return gatewayFaucet{client: client}
}

// chainWide are the refusals every seed would give: the chain, not the seed, refused.
var chainWide = map[chainfaucet.Kind]bool{
	chainfaucet.KindBadRecipient: true,
	chainfaucet.KindBadAmount:    true,
	chainfaucet.KindCooldown:     true,
	chainfaucet.KindEpochCap:     true,
	// A drip that was sent and is not in a block yet: asking another seed could drip twice.
	chainfaucet.KindPending: true,
}

// Fund asks the network's seeds, in order, for amount norama for address.
func (f gatewayFaucet) Fund(ctx context.Context, network *netregistry.Manifest, address string, amount *big.Int) error {
	if len(network.Seeds) == 0 {
		return fmt.Errorf("network %s lists no seed to ask for funds", network.Name)
	}
	var answers []string
	for _, seed := range network.Seeds {
		_, err := chainfaucet.Request(ctx, f.client, "https://"+seed, address, amount)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var refusal *chainfaucet.Refusal
		if errors.As(err, &refusal) && chainWide[refusal.Kind] {
			return fmt.Errorf("the faucet of %s refused: %s (%s)", seed, refusal.Message, refusal.Kind)
		}
		answers = append(answers, seed+": "+httputil.OneLine(err.Error()))
	}
	return fmt.Errorf("none of the %d seeds of %s paid: %s", len(network.Seeds), network.Name, strings.Join(answers, "; "))
}
