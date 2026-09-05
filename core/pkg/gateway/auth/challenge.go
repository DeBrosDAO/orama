package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth/siw"
	"github.com/ethereum/go-ethereum/common"
)

// A login is a wallet signing something this gateway asked it to sign.
//
// What it signs used to be a bare 32-byte nonce. A signature over an opaque
// blob says only "the holder of this key signed these bytes" — it does not say
// who asked, what for, or when, so any signature that wallet ever produced
// anywhere was, in principle, an Orama login, and the dialog the user approved
// showed them a string they had no way to judge. The nonce table made it
// single-use; nothing made it *ours*.
//
// The message is an EIP-4361 (Sign-In with Ethereum) message now, or its Solana
// counterpart, which is the same grammar with one word changed. See the siw
// package for the format. What that buys:
//
//   - the domain in the signed bytes is this gateway's host, so a signature
//     collected anywhere else does not verify here;
//   - the namespace is in the signed bytes, so the user reads which namespace
//     they are signing in to before they approve it;
//   - the timestamps are in the signed bytes, so a captured message states its
//     own deadline rather than relying on the server to remember one.
//
// The nonce table is unchanged and still decides: a message that parses, has
// the right domain and is in date is still not a login until the conditional
// UPDATE in ConsumeNonce claims its row. Everything here is about what the
// signature means; that is about how many times it may mean it.

const (
	// ChallengeTTL is how long a challenge is good for. It is written into the
	// message's Expiration Time and into the nonce row's expires_at, which are
	// checked independently — the message is what the wallet showed the user,
	// the row is what this gateway will honour.
	ChallengeTTL = 5 * time.Minute

	// clockSkew is how far ahead of us a signer's clock may be. It matters
	// only for a wallet that renders its own message and stamps its own Issued
	// At; for the messages we render it is zero by construction.
	clockSkew = 30 * time.Second

	// ethChainID is what goes in the message's Chain ID for an EVM wallet.
	//
	// The session is not bound to any chain: this is a personal_sign over a
	// text message, no transaction and no network is involved, and what the
	// session *is* bound to is the Orama namespace named in the Resources. But
	// the field is mandatory in the grammar, and 1 is what every off-chain
	// sign-in uses, so a wallet rendering the message shows the user something
	// it recognises rather than refusing it.
	ethChainID = "1"

	// solChainID is the same decision on the Solana side, where the SIWS
	// specification enumerates the permitted values.
	solChainID = "mainnet"

	// namespaceResourcePrefix is how the namespace is carried inside the
	// signed bytes. It is a URN because the Resources list is a list of URIs,
	// and it is inside the message rather than beside it in the request body so
	// that the namespace a caller acts on is the namespace the user approved.
	namespaceResourcePrefix = "urn:orama:namespace:"
)

// ErrChallengeMessage means the message a caller presented is not one this
// gateway will act on: it did not parse, it names another domain, or its own
// timestamps put it outside its validity. Callers unwrap it for the specific
// siw error to say which.
var ErrChallengeMessage = errors.New("sign-in message refused")

// ChallengeParams is what a challenge needs to know. Domain and URI come from
// the request that asked for it, so the message names the host the user is
// actually talking to rather than one this gateway believes it is called.
type ChallengeParams struct {
	Wallet    string
	Purpose   string
	Namespace string
	Chain     siw.Chain
	Domain    string
	URI       string
}

// Challenge is what the caller hands to the wallet.
type Challenge struct {
	// Message is the text to sign, exactly. Signing anything else — including
	// a re-rendering of the same fields — produces a signature over different
	// bytes.
	Message string

	Nonce     string
	IssuedAt  time.Time
	ExpiresAt time.Time
	Namespace string
}

// CreateChallenge renders a sign-in message and records the nonce inside it.
//
// The order matters: the row is written last, so a challenge that could not be
// rendered leaves nothing behind for a wallet to answer.
func (s *Service) CreateChallenge(ctx context.Context, p ChallengeParams) (*Challenge, error) {
	namespace := s.nonceNamespace(p.Namespace)

	address, err := canonicalAddress(p.Chain, p.Wallet)
	if err != nil {
		return nil, err
	}

	nonce, err := generateNonce()
	if err != nil {
		return nil, err
	}

	issuedAt := time.Now().UTC().Truncate(time.Second)
	message := &siw.Message{
		Chain:   p.Chain,
		Domain:  p.Domain,
		Address: address,
		// The one line the user reads. It names the namespace because that is
		// the decision they are being asked to approve.
		Statement:      fmt.Sprintf("Sign in to the %s namespace on Orama.", namespace),
		URI:            p.URI,
		ChainID:        chainID(p.Chain),
		Nonce:          nonce,
		IssuedAt:       issuedAt,
		ExpirationTime: issuedAt.Add(ChallengeTTL),
		Resources:      []string{namespaceResourcePrefix + namespace},
	}

	text, err := message.Render()
	if err != nil {
		return nil, fmt.Errorf("build sign-in message: %w", err)
	}

	if err := s.insertNonce(ctx, p.Wallet, nonce, p.Purpose, namespace); err != nil {
		return nil, err
	}

	return &Challenge{
		Message:   text,
		Nonce:     nonce,
		IssuedAt:  issuedAt,
		ExpiresAt: issuedAt.Add(ChallengeTTL),
		Namespace: namespace,
	}, nil
}

// VerifySignedMessage checks that message was signed by the wallet it names,
// for this host, and is in date. It returns the parsed message so the caller
// can read the wallet, nonce and namespace out of the bytes that were signed
// rather than out of the request body beside them.
//
// It says nothing about whether the challenge is still unspent. ConsumeNonce
// decides that, and every caller must run it.
func (s *Service) VerifySignedMessage(ctx context.Context, message, signature, host string) (*siw.Message, error) {
	m, err := siw.Parse(message)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrChallengeMessage, err)
	}
	if err := m.CheckDomain(host); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrChallengeMessage, err)
	}
	if err := m.CheckFreshness(time.Now().UTC(), clockSkew); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrChallengeMessage, err)
	}

	// The chain comes from the message's own header line, so a caller cannot
	// ask for the cheaper verification of the two.
	var verified bool
	switch m.Chain {
	case siw.Ethereum:
		verified, err = s.verifyEthSignature(m.Address, message, signature)
	case siw.Solana:
		verified, err = s.verifySolSignature(m.Address, message, signature)
	default:
		return nil, fmt.Errorf("%w: unsupported chain %q", ErrChallengeMessage, m.Chain)
	}
	if err != nil {
		return nil, fmt.Errorf("signature verification failed: %w", err)
	}
	if !verified {
		return nil, errors.New("signature verification failed")
	}
	return m, nil
}

// NamespaceOf reads the namespace out of a verified message.
//
// A message with no namespace resource is one this gateway did not issue: every
// challenge it renders carries exactly one.
func NamespaceOf(m *siw.Message) (string, error) {
	var found string
	for _, r := range m.Resources {
		ns, ok := strings.CutPrefix(r, namespaceResourcePrefix)
		if !ok {
			continue
		}
		if found != "" {
			return "", fmt.Errorf("%w: names more than one namespace", ErrChallengeMessage)
		}
		found = ns
	}
	if found == "" {
		return "", fmt.Errorf("%w: names no namespace", ErrChallengeMessage)
	}
	return found, nil
}

// generateNonce returns 256 bits of randomness as hex.
//
// Hex rather than base64url because the grammar's nonce is 8*( ALPHA / DIGIT ):
// the '-' and '_' of base64url are not alphanumeric, so a base64url nonce makes
// a message that a conforming wallet is entitled to refuse.
func generateNonce() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// canonicalAddress renders the wallet the way the message must carry it.
func canonicalAddress(chain siw.Chain, wallet string) (string, error) {
	wallet = strings.TrimSpace(wallet)
	switch chain {
	case siw.Ethereum:
		if !common.IsHexAddress(wallet) {
			return "", fmt.Errorf("%w: %q is not an Ethereum address", ErrChallengeMessage, wallet)
		}
		// EIP-4361 requires the EIP-55 checksummed form, and the wallet
		// address arrives in whatever case the caller typed.
		return common.HexToAddress(wallet).Hex(), nil
	case siw.Solana:
		return wallet, nil
	default:
		return "", fmt.Errorf("%w: unsupported chain %q", ErrChallengeMessage, chain)
	}
}

func chainID(chain siw.Chain) string {
	if chain == siw.Solana {
		return solChainID
	}
	return ethChainID
}

// ParseChain reads the chain a caller asked for. Empty means Ethereum, which is
// what every Orama client has ever used.
func ParseChain(s string) (siw.Chain, error) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "", "ETH", "ETHEREUM":
		return siw.Ethereum, nil
	case "SOL", "SOLANA":
		return siw.Solana, nil
	default:
		return "", fmt.Errorf("unsupported chain_type %q: use ETH or SOL", s)
	}
}
