package chainfaucet

import (
	"errors"
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/httputil"
	"github.com/DeBrosOfficial/network/pkg/onchain"
)

// Kind says why a drip was not made, in terms a client can act on.
type Kind string

const (
	// KindBadRecipient: the recipient is not an account that can be funded.
	KindBadRecipient Kind = "bad_recipient"
	// KindBadAmount: the amount is not positive, or is over the chain's maximum drip.
	KindBadAmount Kind = "bad_amount"
	// KindCooldown: the recipient drew inside the chain's per-recipient cooldown.
	KindCooldown Kind = "cooldown"
	// KindEpochCap: the faucet has minted the epoch's cap across all recipients.
	KindEpochCap Kind = "epoch_cap"
	// KindDisabled: this chain does not run a faucet (a production chain id, or the genesis
	// left faucet_enabled off).
	KindDisabled Kind = "faucet_disabled"
	// KindBusy: too many drips are waiting for the faucet's turn.
	KindBusy Kind = "busy"
	// KindUnavailable: the faucet cannot pay for the drip, or cannot reach its chain.
	KindUnavailable Kind = "unavailable"
	// KindPending: the drip was sent and is not in a block yet.
	KindPending Kind = "pending"
)

// maxDetail bounds the chain's reason a Refusal quotes.
const maxDetail = 240

// Refusal is a drip that was not made. Its message is fixed text and, for the refusals the chain
// explains (a cooldown's next time, the maximum drip), the chain's reason on one line.
type Refusal struct {
	Kind    Kind
	Message string
}

func (r *Refusal) Error() string { return string(r.Kind) + ": " + r.Message }

func refuse(kind Kind, format string, args ...any) *Refusal {
	return &Refusal{Kind: kind, Message: fmt.Sprintf(format, args...)}
}

// chainReasons are the refusals x/emission gives a MsgFaucet (chain/x/emission/types/errors.go),
// by the text of each registered error, and what each means to a client.
// TestChainReasons_matchTheChain fails when the chain's texts and these differ.
var chainReasons = []struct {
	text string
	kind Kind
}{
	{"the faucet runs only on a devnet, stagenet or localnet chain-id", KindDisabled},
	{"the faucet is disabled", KindDisabled},
	{"faucet amount must be positive and at most faucet_max_drip", KindBadAmount},
	{"faucet recipient is invalid or a blocked address", KindBadRecipient},
	{"faucet recipient is still within its cooldown", KindCooldown},
	{"faucet epoch cap exceeded", KindEpochCap},
}

// classify turns an error from sending a faucet transaction into a Refusal when the chain refused
// the drip or the faucet cannot pay for it, and returns nil for anything else (a chain that could
// not be reached, a node that answered nonsense), which the caller reports as a fault.
func classify(err error, faucetAddress string) *Refusal {
	if errors.Is(err, onchain.ErrAccountNotFound) {
		return refuse(KindUnavailable, "the faucet account %s does not exist on this chain yet: its operator must fund it", faucetAddress)
	}
	line := httputil.OneLine(err.Error())
	// The chain's texts are registered errors and appear verbatim, so they are found as they are
	// written; lower-casing the line could change its length and with it the offsets.
	for _, r := range chainReasons {
		if at := strings.Index(line, r.text); at >= 0 {
			return &Refusal{Kind: r.kind, Message: reasonFrom(line[at:])}
		}
	}
	if text := strings.ToLower(line); strings.Contains(text, "insufficient funds") || strings.Contains(text, "insufficient fee") {
		return refuse(KindUnavailable, "the faucet account %s cannot pay the transaction fee: its operator must fund it", faucetAddress)
	}
	return nil
}

// statusSuffix ends the chain's reason in a simulation refusal: the HTTP status error that carried it.
const statusSuffix = " (chain API returned HTTP"

// reasonFrom is the chain's reason, starting at line, cut before the status error that wraps it and
// bounded. It is text a node produced.
func reasonFrom(line string) string {
	if at := strings.Index(line, statusSuffix); at >= 0 {
		line = line[:at]
	}
	return httputil.PrintableMax(line, maxDetail)
}
