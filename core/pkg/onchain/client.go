package onchain

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/DeBrosOfficial/network/pkg/clusterreg"
	"github.com/DeBrosOfficial/network/pkg/httputil"
	"github.com/DeBrosOfficial/network/pkg/rwagent"
)

const (
	// GasSafetyNumerator and GasSafetyDenominator scale the gas a simulation
	// used into the gas limit: 1.5x. A simulation does not run signature
	// verification on the real signature, and the state can move between the
	// simulation and the block.
	GasSafetyNumerator   = 3
	GasSafetyDenominator = 2

	// FeeMarginNumerator and FeeMarginDenominator scale gas x base fee into the
	// fee: 1.5x. The base fee moves with every block and the transaction is
	// included a few blocks after it was read; a fee under the base fee of the
	// block that runs it is refused. What exceeds the base fee is a tip to the
	// proposer.
	FeeMarginNumerator   = 3
	FeeMarginDenominator = 2

	// DefaultMaxFeeNorama is the most a transaction may pay in fee unless the caller raises the
	// limit (Client.SetMaxFee): 1 ORAMA, hundreds of times a normal fee. A chain, a gateway or a
	// base fee that asks for more is faulty or hostile, and the wallet must not sign it by default.
	DefaultMaxFeeNorama = 1_000_000_000

	// maxBaseFeeDigits bounds a base fee read from a chain or a gateway. The fee is norama per gas
	// and is a few digits in practice; a longer number is an attack on the arithmetic or a fault.
	maxBaseFeeDigits = 18

	// maxChainIDLen is the longest chain id the client accepts.
	maxChainIDLen = 64

	// simulationGasLimit and simulationFee fill the gas and fee fields of the
	// transaction that is simulated: the chain does not check either in a
	// simulation, and the builder needs both positive.
	simulationGasLimit = 10_000_000
	simulationFee      = "1"
	// signatureLen is the size of the placeholder signature of a simulation.
	signatureLen = 64
)

var (
	baseFeePattern = regexp.MustCompile(`^[0-9]{1,` + fmt.Sprint(maxBaseFeeDigits) + `}$`)
	// chainIDPattern is the characters of a chain id. Anything else could carry a control
	// character to the terminal where the chain id is shown for approval.
	chainIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,` + fmt.Sprint(maxChainIDLen) + `}$`)
)

// ValidChainID reports whether id is a chain id the client signs for: 1 to 64 characters of
// A-Z, a-z, 0-9, '.', '_' and '-'. It is the one definition; callers that read a chain id from an
// endpoint check it with this before they show it.
func ValidChainID(id string) bool { return chainIDPattern.MatchString(id) }

// SentError is an error after the transaction was broadcast: the chain took it (or may have, when
// the answer to the broadcast itself was lost), so it may still be in a block whatever went wrong
// afterwards (a wait that ended, a lookup that failed). A caller that
// must not send the same thing twice, or must not take back what it charged for it, tells this from
// an error before the broadcast, where nothing was sent. A transaction that is in a block and failed
// there is a SentError too; errors.Is(err, clusterreg.ErrTxFailed) tells it apart.
type SentError struct {
	// Hash is the transaction's hash.
	Hash string
	Err  error
}

func (e *SentError) Error() string { return e.Err.Error() }
func (e *SentError) Unwrap() error { return e.Err }

// ErrAccountNotFound says the signing account is not on the chain yet. An
// account exists once it has received funds, and only an existing account can
// pay a fee.
var ErrAccountNotFound = errors.New("the signing account does not exist on the chain yet")

// Receipt is a transaction that is in a block.
type Receipt struct {
	// Hash is the transaction hash.
	Hash string
	// Height is the block that holds it.
	Height int64
	// Gas and Fee are what the transaction declared.
	Gas uint64
	Fee string
}

// Client sends one operator's transactions to one chain.
type Client struct {
	chain   Chain
	signer  Signer
	chainID string
	maxFee  *big.Int

	mu       sync.Mutex
	identity *rwagent.OramaAccount
}

// New returns a Client for chainID. The signer is the operator's RootWallet.
func New(chain Chain, signer Signer, chainID string) (*Client, error) {
	if chain == nil || signer == nil {
		return nil, errors.New("a chain and a signer are required")
	}
	if !chainIDPattern.MatchString(chainID) {
		return nil, fmt.Errorf("chain id %q must be 1 to %d characters of A-Z, a-z, 0-9, '.', '_' and '-'", httputil.Printable(chainID), maxChainIDLen)
	}
	return &Client{chain: chain, signer: signer, chainID: chainID, maxFee: big.NewInt(DefaultMaxFeeNorama)}, nil
}

// SetMaxFee sets the most a transaction may pay in fee, in norama (DefaultMaxFeeNorama until it is
// called). A transaction whose fee would be higher is refused before it is signed. Call it before
// the first transaction.
func (c *Client) SetMaxFee(norama *big.Int) error {
	if norama == nil || norama.Sign() <= 0 {
		return errors.New("the fee limit must be a positive amount of norama")
	}
	c.maxFee = new(big.Int).Set(norama)
	return nil
}

// ChainID is the chain the client signs for.
func (c *Client) ChainID() string { return c.chainID }

// Operator returns the address of the account that signs: the operator.
func (c *Client) Operator(ctx context.Context) (string, error) {
	id, err := c.account(ctx)
	if err != nil {
		return "", err
	}
	return id.Address, nil
}

// account asks the RootWallet for the signing account once and remembers it, so
// a run of transactions is one request to the agent and not one per message. The
// signature of every transaction is still checked against it.
func (c *Client) account(ctx context.Context) (*rwagent.OramaAccount, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.identity != nil {
		return c.identity, nil
	}
	id, err := c.signer.OramaAccount(ctx)
	if err != nil {
		return nil, fmt.Errorf("read the RootWallet's orama account: %w", err)
	}
	c.identity = id
	return id, nil
}

// Prepared is a transaction that has its account, gas and fee and is not yet signed. Gas and Fee
// are what Submit will sign, so a caller can show them for approval first.
type Prepared struct {
	// Gas is the gas limit and Fee the fee in norama the transaction will declare.
	Gas uint64
	Fee string

	// what names the transaction in an error ("send 5 norama to orama1..."); empty for none.
	what   string
	client *Client
	tx     clusterreg.Direct
	id     *rwagent.OramaAccount
}

// prepare reads the signing account, simulates the transaction and prices it.
func (c *Client) prepare(ctx context.Context, typeURL string, msg []byte) (*Prepared, error) {
	id, err := c.account(ctx)
	if err != nil {
		return nil, err
	}
	acct, err := c.chain.Account(ctx, id.Address)
	if err != nil {
		return nil, accountError(id.Address, err)
	}
	if len(acct.PubKey) != 0 && !bytes.Equal(acct.PubKey, id.PubKey) {
		return nil, fmt.Errorf("account %s is known to the chain under another public key than the RootWallet's", id.Address)
	}
	latest, err := c.chain.LatestHeight(ctx)
	if err != nil {
		return nil, fmt.Errorf("read the newest block's height for the timeout height: %w", err)
	}
	tx := clusterreg.Direct{
		TypeURL: typeURL, Msg: msg, PubKey: id.PubKey, Sequence: acct.Sequence,
		ChainID: c.chainID, AccountNumber: acct.Number, TimeoutHeight: clusterreg.TimeoutHeightAfter(latest),
	}
	if tx.Gas, err = c.gasFor(ctx, tx); err != nil {
		return nil, err
	}
	if tx.FeeAmount, err = c.feeFor(ctx, tx.Gas); err != nil {
		return nil, err
	}
	return &Prepared{Gas: tx.Gas, Fee: tx.FeeAmount, client: c, tx: tx, id: id}, nil
}

// send builds, signs, broadcasts and waits for one message from the signing
// account.
func (c *Client) send(ctx context.Context, typeURL string, msg []byte) (*Receipt, error) {
	p, err := c.prepare(ctx, typeURL, msg)
	if err != nil {
		return nil, err
	}
	return p.Submit(ctx)
}

func accountError(address string, err error) error {
	var status *clusterreg.StatusError
	if errors.As(err, &status) && status.Code == http.StatusNotFound {
		return fmt.Errorf("%w: %s must receive funds first (on a test network, from its faucet): %w", ErrAccountNotFound, address, err)
	}
	return fmt.Errorf("read the chain account of %s: %w", address, err)
}

// gasFor simulates tx and returns the gas limit to declare.
func (c *Client) gasFor(ctx context.Context, tx clusterreg.Direct) (uint64, error) {
	tx.Gas, tx.FeeAmount = simulationGasLimit, simulationFee
	raw, err := tx.TxRaw(make([]byte, signatureLen))
	if err != nil {
		return 0, fmt.Errorf("build the transaction to simulate: %w", err)
	}
	used, err := c.chain.SimulateGas(ctx, raw)
	if err != nil {
		return 0, fmt.Errorf("simulate the transaction: %w", err)
	}
	if used == 0 {
		return 0, errors.New("the simulation reported no gas used, so no gas limit can be derived from it")
	}
	gas, err := scaleUp(used, GasSafetyNumerator, GasSafetyDenominator)
	if err != nil {
		return 0, fmt.Errorf("the simulation reported %d gas used: %w", used, err)
	}
	return gas, nil
}

// feeFor is the fee for a transaction of gas: the base fee now, times gas, times the margin, and at
// least 1 norama because a fee must be positive. A base fee that is not a plain number of at most
// maxBaseFeeDigits digits, or a fee over the client's limit, is an error and not a transaction.
func (c *Client) feeFor(ctx context.Context, gas uint64) (string, error) {
	baseFee, err := c.chain.BaseFee(ctx)
	if err != nil {
		return "", fmt.Errorf("read the base fee: %w", err)
	}
	perGas, err := ParseBaseFee(baseFee)
	if err != nil {
		return "", err
	}
	fee := new(big.Int).Mul(perGas, new(big.Int).SetUint64(gas))
	fee = ceilDiv(fee.Mul(fee, big.NewInt(FeeMarginNumerator)), big.NewInt(FeeMarginDenominator))
	if fee.Sign() == 0 {
		return "1", nil
	}
	if fee.Cmp(c.maxFee) > 0 {
		return "", fmt.Errorf("the fee for this transaction would be %s norama (%d gas at a base fee of %s norama), over the limit of %s norama; "+
			"this is far above a normal fee, so the chain or its gateway may be faulty or hostile; raise the limit only if you trust it",
			fee, gas, perGas, c.maxFee)
	}
	return fee.String(), nil
}

// ParseBaseFee reads a base fee a chain or a gateway answered: norama per gas, a plain non-negative
// integer of at most maxBaseFeeDigits digits. A sign, a prefix, an exponent or a longer number is
// refused, since the fee is computed from it and signed.
func ParseBaseFee(s string) (*big.Int, error) {
	if !baseFeePattern.MatchString(s) {
		return nil, fmt.Errorf("the chain's base fee %q is not a plain integer of at most %d digits", httputil.Printable(s), maxBaseFeeDigits)
	}
	n, _ := new(big.Int).SetString(s, 10)
	return n, nil
}

// Submit has the RootWallet sign the prepared transaction, broadcasts it and waits for its block.
// The hash the chain answers must be the hash of the bytes that were sent: a node that answers
// another hash would have the caller wait on, and report, some other transaction.
func (p *Prepared) Submit(ctx context.Context) (*Receipt, error) {
	receipt, err := p.submit(ctx)
	if err != nil && p.what != "" {
		return nil, fmt.Errorf("%s: %w", p.what, err)
	}
	return receipt, err
}

func (p *Prepared) submit(ctx context.Context) (*Receipt, error) {
	c, tx, id := p.client, p.tx, p.id
	doc, err := tx.SignDoc()
	if err != nil {
		return nil, fmt.Errorf("build the sign document: %w", err)
	}
	sig, err := c.signer.SignOramaTx(ctx, doc)
	if err != nil {
		return nil, fmt.Errorf("sign the transaction: %w", err)
	}
	if sig.Address != id.Address || !bytes.Equal(sig.PubKey, id.PubKey) {
		return nil, fmt.Errorf("the RootWallet signed as %s, not as the account %s it reported", httputil.Printable(sig.Address), id.Address)
	}
	raw, err := tx.TxRaw(sig.Signature)
	if err != nil {
		return nil, fmt.Errorf("build the transaction: %w", err)
	}
	local := clusterreg.TxHash(raw)
	answered, err := c.chain.Broadcast(ctx, raw)
	if err != nil {
		err = fmt.Errorf("broadcast the transaction: %w", err)
		if clusterreg.NotSent(err) {
			return nil, err
		}
		// The node may have taken it before the answer was lost.
		return nil, &SentError{Hash: local, Err: err}
	}
	if !strings.EqualFold(answered, local) {
		return nil, &SentError{Hash: local, Err: fmt.Errorf("the chain answered the transaction hash %q for a transaction whose hash is %s; refusing to follow or report it",
			httputil.Printable(answered), local)}
	}
	height, err := c.chain.WaitIncluded(ctx, local)
	if err != nil {
		return nil, &SentError{Hash: local, Err: fmt.Errorf("transaction %s: %w", local, err)}
	}
	return &Receipt{Hash: local, Height: height, Gas: tx.Gas, Fee: tx.FeeAmount}, nil
}

// scaleUp returns ceil(v * num / den), or an error when it does not fit a uint64.
func scaleUp(v uint64, num, den uint64) (uint64, error) {
	scaled := new(big.Int).Mul(new(big.Int).SetUint64(v), new(big.Int).SetUint64(num))
	out := ceilDiv(scaled, new(big.Int).SetUint64(den))
	if !out.IsUint64() {
		return 0, fmt.Errorf("%d scaled by %d/%d does not fit the gas limit", v, num, den)
	}
	return out.Uint64(), nil
}

func ceilDiv(a, b *big.Int) *big.Int {
	q, r := new(big.Int).QuoRem(a, b, new(big.Int))
	if r.Sign() > 0 {
		q.Add(q, big.NewInt(1))
	}
	return q
}
