package chainread

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"mime"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/DeBrosOfficial/network/pkg/chainfaucet"
	"github.com/DeBrosOfficial/network/pkg/gateway/clientkey"
)

// The faucet route of a test network's gateway:
//
//	POST /v1/chain/faucet  {"recipient":"orama1...", "amount":"<norama>"}
//
// It exists only on a gateway the node was configured to give a faucet key (node.yaml
// chain.faucet): without one the path is as unknown as any other. The gateway signs MsgFaucet for
// the recipient with that key and answers when the transaction is in a block. The chain polices
// the drip (chainfaucet); this route bounds the request: the body, the requests in flight, and,
// in the gateway's rate limiter, the rate.
//
// The answer is JSON: {"tx_hash","amount","height"} for a drip (the amount and height as decimal
// strings, as proto3 JSON writes 64-bit integers), and {"error","message"} for anything else, the
// error one of the chainfaucet kinds or bad_request.
const (
	faucetPath = "faucet"

	// faucetMaxBody bounds the request: an address and an amount.
	faucetMaxBody = 1 << 10
	// faucetMaxConcurrent is the requests in flight at once: the drips that may wait
	// (chainfaucet.QueueDepth) and the one being made, with room for requests the service answers
	// at once. The rest are answered 503 with Retry-After.
	faucetMaxConcurrent = chainfaucet.QueueDepth + 8
	// faucetWait is how long a request waits for its drip to be in a block. A request that waits
	// longer is answered pending and the drip goes on.
	faucetWait = 45 * time.Second

	errBadRequest = "bad_request"
)

// FaucetService makes the drips; *chainfaucet.Service is one.
type FaucetService interface {
	Drip(ctx context.Context, recipient string, amount *big.Int) (*chainfaucet.Dripped, error)
}

// faucetRequest is the body of the route.
type faucetRequest struct {
	Recipient string `json:"recipient"`
	// Amount is norama as a decimal string; empty is chainfaucet.DefaultDripNorama.
	Amount string `json:"amount"`
}

// faucetAnswer is a drip that is in a block.
type faucetAnswer struct {
	TxHash string `json:"tx_hash"`
	Amount string `json:"amount"`
	Height string `json:"height"`
}

// faucetError is any other answer.
type faucetError struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

var faucetAmountPattern = regexp.MustCompile(`^[1-9][0-9]{0,` + strconv.Itoa(chainfaucet.MaxAmountDigits-1) + `}$`)

func (p *Proxy) serveFaucet(w http.ResponseWriter, r *http.Request) {
	if p.faucet == nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	select {
	case p.faucetSlots <- struct{}{}:
	default:
		w.Header().Set("Retry-After", txBusyRetryAfter)
		writeFaucetError(w, http.StatusServiceUnavailable, string(chainfaucet.KindBusy), "too many drips in flight")
		return
	}
	defer func() { <-p.faucetSlots }()
	req, amount, ok := readFaucetRequest(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), faucetWait)
	defer cancel()
	// The allowance is charged before the drip and given back if it is not made, so a refused
	// request costs the client nothing; a drip that is sent and not yet in a block stays charged.
	client := clientkey.BucketKey(clientkey.Attribute(r))
	charge, wait, message, ok := p.chargeFaucet(client, amount)
	if !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		writeFaucetError(w, http.StatusTooManyRequests, string(chainfaucet.KindAllowance), message)
		return
	}
	dripped, err := p.faucet.Drip(ctx, req.Recipient, amount)
	if err != nil {
		if !isPending(err) {
			p.refundFaucet(charge)
		}
		writeFaucetFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, faucetAnswer{TxHash: dripped.TxHash, Amount: dripped.Amount.String(), Height: strconv.FormatInt(dripped.Height, 10)})
}

// faucetCeilingKey is the one key the gateway-wide ceiling is held under.
const faucetCeilingKey = "gateway"

// faucetCharge is what one request took from the gateway's ceiling and from its client's allowance.
type faucetCharge struct{ ceiling, client chainfaucet.Ticket }

// chargeFaucet charges amount to the gateway's ceiling and to client's allowance, or to neither. It
// says how long until the one that refused gives its allowance back, and what to tell the client.
func (p *Proxy) chargeFaucet(client string, amount *big.Int) (charge faucetCharge, wait time.Duration, message string, ok bool) {
	if charge.ceiling, wait, ok = p.faucetCeiling.Take(faucetCeilingKey, amount); !ok {
		return charge, wait, "this gateway has given out all it gives of the faucet for now; try again later or ask another gateway", false
	}
	if charge.client, wait, ok = p.faucetBudget.Take(client, amount); !ok {
		p.faucetCeiling.Return(charge.ceiling)
		return charge, wait, "this network has asked for its whole allowance of the faucet for now; try again later", false
	}
	return charge, 0, "", true
}

// refundFaucet gives back what chargeFaucet took for a drip that was not made.
func (p *Proxy) refundFaucet(c faucetCharge) {
	p.faucetBudget.Return(c.client)
	p.faucetCeiling.Return(c.ceiling)
}

// isPending reports a drip that was sent and is not in a block yet.
func isPending(err error) bool {
	var refusal *chainfaucet.Refusal
	return errors.As(err, &refusal) && refusal.Kind == chainfaucet.KindPending
}

// readFaucetRequest checks the method, the content type and the body, and returns the request and
// the amount to drip. It writes the refusal itself and reports false.
func readFaucetRequest(w http.ResponseWriter, r *http.Request) (faucetRequest, *big.Int, bool) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeFaucetError(w, http.StatusMethodNotAllowed, errBadRequest, "use POST")
		return faucetRequest{}, nil, false
	}
	if r.URL.RawQuery != "" {
		writeFaucetError(w, http.StatusBadRequest, errBadRequest, "the route takes no query")
		return faucetRequest{}, nil, false
	}
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		writeFaucetError(w, http.StatusUnsupportedMediaType, errBadRequest, "send application/json")
		return faucetRequest{}, nil, false
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, faucetMaxBody+1))
	if err != nil || len(body) > faucetMaxBody {
		writeFaucetError(w, http.StatusRequestEntityTooLarge, errBadRequest, "request too large")
		return faucetRequest{}, nil, false
	}
	var req faucetRequest
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil || dec.Decode(&struct{}{}) != io.EOF {
		writeFaucetError(w, http.StatusBadRequest, errBadRequest, `send {"recipient":"orama1...","amount":"<norama>"}; amount is optional and a string`)
		return faucetRequest{}, nil, false
	}
	amount, ok := faucetAmount(req.Amount)
	if !ok {
		writeFaucetError(w, http.StatusBadRequest, string(chainfaucet.KindBadAmount), "amount must be a whole number of norama, as a string, without a sign or leading zeros")
		return faucetRequest{}, nil, false
	}
	return req, amount, true
}

// faucetAmount is the amount a request asks for: the default when it names none.
func faucetAmount(s string) (*big.Int, bool) {
	if s == "" {
		return big.NewInt(chainfaucet.DefaultDripNorama), true
	}
	if !faucetAmountPattern.MatchString(s) {
		return nil, false
	}
	return new(big.Int).SetString(s, 10)
}

// faucetStatus is the HTTP status of each kind of refusal.
var faucetStatus = map[chainfaucet.Kind]int{
	chainfaucet.KindBadRecipient: http.StatusBadRequest,
	chainfaucet.KindBadAmount:    http.StatusBadRequest,
	chainfaucet.KindCooldown:     http.StatusTooManyRequests,
	chainfaucet.KindAllowance:    http.StatusTooManyRequests,
	chainfaucet.KindEpochCap:     http.StatusServiceUnavailable,
	chainfaucet.KindDisabled:     http.StatusForbidden,
	chainfaucet.KindBusy:         http.StatusServiceUnavailable,
	chainfaucet.KindUnavailable:  http.StatusServiceUnavailable,
	chainfaucet.KindPending:      http.StatusGatewayTimeout,
}

// writeFaucetFailure answers a drip that was not made. A refusal is explained in its own words; a
// fault is not: what the faucet could not do against its chain is in the log, and the requester is
// told only that it could not.
func writeFaucetFailure(w http.ResponseWriter, err error) {
	var refusal *chainfaucet.Refusal
	if errors.As(err, &refusal) {
		status, known := faucetStatus[refusal.Kind]
		if !known {
			status = http.StatusInternalServerError
		}
		if refusal.Kind == chainfaucet.KindBusy {
			w.Header().Set("Retry-After", txBusyRetryAfter)
		}
		writeFaucetError(w, status, string(refusal.Kind), refusal.Message)
		return
	}
	writeFaucetError(w, http.StatusBadGateway, "faucet_failed", "the faucet could not make the drip; try again later")
}

func writeFaucetError(w http.ResponseWriter, status int, kind, message string) {
	writeJSON(w, status, faucetError{Error: kind, Message: message})
}
