// Package siw builds and parses Sign-In-With messages: EIP-4361 (Sign-In with
// Ethereum) and its Solana counterpart, which is the same grammar with one word
// changed in the header line.
//
// The reason the format exists is that a bare signature is a permanent,
// context-free credential. "The holder of this key signed these bytes" says
// nothing about *what for*, *for whom*, or *when* — so a signature captured
// anywhere is a valid login everywhere, and the user approving it is shown an
// opaque blob and asked to trust it. Everything in this grammar is there to
// answer one of those three questions inside the signed bytes themselves: the
// domain says who asked, the nonce and the timestamps say when and once, and
// the statement says what the user is agreeing to in words they can read.
//
// This package is deliberately free of database and HTTP concerns. It renders,
// it parses, and it answers questions about a message's own contents. Whether
// the nonce was ever issued, and whether it has already been spent, is the
// nonce table's business (see nonce.go) — a message that parses perfectly is
// still not a login.
package siw

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

// Chain is the key type a message is signed with. It is carried in the header
// line, so a parsed message says for itself which verification a signature
// needs — the caller does not get to assert it.
type Chain string

const (
	Ethereum Chain = "ETH"
	Solana   Chain = "SOL"
)

// chainWord is the word the header line uses for each chain.
var chainWord = map[Chain]string{
	Ethereum: "Ethereum",
	Solana:   "Solana",
}

// Version is the only message version EIP-4361 defines.
const Version = "1"

// MinNonceLength is the grammar's floor: 8*( ALPHA / DIGIT ). Orama issues far
// longer nonces; this is what the parser refuses below.
const MinNonceLength = 8

// TimeFormat is how the timestamps are written. RFC 3339 with second
// resolution, which is what every reference implementation emits.
const TimeFormat = time.RFC3339

// Sentinel errors for the checks a caller performs against a parsed message.
// They are distinct so a refusal can say which one failed without the caller
// matching on prose.
var (
	// ErrDomainMismatch means the message names a different host than the one
	// serving the request. This is the check that stops a signature collected
	// by one site from logging in at another.
	ErrDomainMismatch = errors.New("the message was signed for a different domain")

	// ErrExpired means the message's own expiration time has passed.
	ErrExpired = errors.New("the signed message has expired")

	// ErrNotYetValid means the message carries a Not Before that has not
	// arrived.
	ErrNotYetValid = errors.New("the signed message is not valid yet")

	// ErrIssuedInFuture means the message claims to have been issued later than
	// now. A message we issued cannot be, so this is either a forgery or a
	// clock that has moved.
	ErrIssuedInFuture = errors.New("the signed message claims a future issue time")
)

// ParseError says which field of a message is wrong and why. The field name is
// the label as it appears in the message, so the error can be read next to the
// text it came from.
type ParseError struct {
	Field  string
	Reason string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("sign-in message: %s: %s", e.Field, e.Reason)
}

// Message is one Sign-In-With message.
//
// The zero values of ExpirationTime, NotBefore and the empty Statement,
// RequestID and Resources are how "this optional field is absent" is spelled;
// Render omits them and Parse leaves them unset.
type Message struct {
	Chain     Chain
	Domain    string
	Address   string
	Statement string
	URI       string
	ChainID   string
	Nonce     string
	IssuedAt  time.Time

	ExpirationTime time.Time
	NotBefore      time.Time
	RequestID      string
	Resources      []string
}

// Render writes the message in the exact wire format, validating first: a
// message that would not parse must not be handed to a wallet, because the
// wallet is the one that has to display it.
func (m *Message) Render() (string, error) {
	if err := m.Validate(); err != nil {
		return "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s wants you to sign in with your %s account:\n", m.Domain, chainWord[m.Chain])
	b.WriteString(m.Address + "\n")
	b.WriteString("\n")
	if m.Statement != "" {
		b.WriteString(m.Statement + "\n")
	}
	b.WriteString("\n")
	b.WriteString("URI: " + m.URI + "\n")
	b.WriteString("Version: " + Version + "\n")
	b.WriteString("Chain ID: " + m.ChainID + "\n")
	b.WriteString("Nonce: " + m.Nonce + "\n")
	b.WriteString("Issued At: " + m.IssuedAt.UTC().Format(TimeFormat))
	if !m.ExpirationTime.IsZero() {
		b.WriteString("\nExpiration Time: " + m.ExpirationTime.UTC().Format(TimeFormat))
	}
	if !m.NotBefore.IsZero() {
		b.WriteString("\nNot Before: " + m.NotBefore.UTC().Format(TimeFormat))
	}
	if m.RequestID != "" {
		b.WriteString("\nRequest ID: " + m.RequestID)
	}
	if len(m.Resources) > 0 {
		b.WriteString("\nResources:")
		for _, r := range m.Resources {
			b.WriteString("\n- " + r)
		}
	}
	return b.String(), nil
}

// Validate enforces the grammar's own rules on a message's fields, whether it
// was just built or just parsed.
func (m *Message) Validate() error {
	if _, ok := chainWord[m.Chain]; !ok {
		return &ParseError{Field: "chain", Reason: fmt.Sprintf("unknown chain %q", m.Chain)}
	}
	if err := validateDomain(m.Domain); err != nil {
		return err
	}
	if err := validateAddress(m.Chain, m.Address); err != nil {
		return err
	}
	if strings.ContainsAny(m.Statement, "\n\r") {
		return &ParseError{Field: "statement", Reason: "contains a line break, which would let it forge the fields below it"}
	}
	if err := validateURI(m.URI); err != nil {
		return err
	}
	if strings.TrimSpace(m.ChainID) == "" {
		return &ParseError{Field: "Chain ID", Reason: "is empty"}
	}
	if err := validateNonce(m.Nonce); err != nil {
		return err
	}
	if m.IssuedAt.IsZero() {
		return &ParseError{Field: "Issued At", Reason: "is missing"}
	}
	if !m.ExpirationTime.IsZero() && !m.ExpirationTime.After(m.IssuedAt) {
		return &ParseError{Field: "Expiration Time", Reason: "is not after Issued At, so the message is born expired"}
	}
	for _, r := range m.Resources {
		if strings.TrimSpace(r) == "" || strings.ContainsAny(r, "\n\r") {
			return &ParseError{Field: "Resources", Reason: "holds an empty or multi-line entry"}
		}
	}
	return nil
}

// CheckDomain reports whether the message was signed for host.
//
// This is the check the whole format is for. Without it a signature the user
// made on any other site — or on a phishing page that asked for one — is a
// valid Orama login, because the bytes carry no opinion about who asked.
func (m *Message) CheckDomain(host string) error {
	if !strings.EqualFold(m.Domain, strings.TrimSpace(host)) {
		return fmt.Errorf("%w: signed for %q, presented to %q", ErrDomainMismatch, m.Domain, host)
	}
	return nil
}

// CheckFreshness applies the message's own timestamps.
//
// skew is how far ahead of us the signer's clock may be. It applies only to the
// near edge — Issued At and Not Before — because that is the only edge where a
// clock difference refuses a login that should have worked: a wallet that
// renders its own timestamps stamps them from its own clock.
//
// It deliberately does not apply at the expiry. Extending an expiry by the
// tolerance widens the window in which a captured message still works, and buys
// nothing: the nonce row behind the message is checked against this gateway's
// clock with no tolerance at all, so a message whose expiry has passed is
// already dead by the only measure that decides.
func (m *Message) CheckFreshness(now time.Time, skew time.Duration) error {
	if m.IssuedAt.After(now.Add(skew)) {
		return fmt.Errorf("%w: issued at %s", ErrIssuedInFuture, m.IssuedAt.UTC().Format(TimeFormat))
	}
	if !m.NotBefore.IsZero() && now.Add(skew).Before(m.NotBefore) {
		return fmt.Errorf("%w: valid from %s", ErrNotYetValid, m.NotBefore.UTC().Format(TimeFormat))
	}
	if !m.ExpirationTime.IsZero() && now.After(m.ExpirationTime) {
		return fmt.Errorf("%w: expired at %s", ErrExpired, m.ExpirationTime.UTC().Format(TimeFormat))
	}
	return nil
}

const headerSuffix = " account:"

// Parse reads a message off the wire.
//
// It is strict on purpose. Every field it accepts is a field a caller will make
// a decision on, and a parser that guesses at a malformed message is a parser
// that can be steered — the classic version being a statement carrying a
// newline, so that the text the user reads and the fields the server acts on
// are not the same text.
func Parse(text string) (*Message, error) {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	// The shortest legal message is header, address, blank, blank, and the five
	// mandatory labelled lines.
	if len(lines) < 9 {
		return nil, &ParseError{Field: "message", Reason: "is too short to be a sign-in message"}
	}

	m := &Message{}
	var err error
	if m.Chain, m.Domain, err = parseHeader(lines[0]); err != nil {
		return nil, err
	}
	m.Address = lines[1]

	if lines[2] != "" {
		return nil, &ParseError{Field: "message", Reason: "the line after the address must be blank"}
	}

	// LF [statement LF] LF — a blank line at index 3 means there is no
	// statement, anything else is the statement itself.
	i := 3
	if lines[i] != "" {
		m.Statement = lines[i]
		i++
	}
	if i >= len(lines) || lines[i] != "" {
		return nil, &ParseError{Field: "statement", Reason: "must be followed by a blank line"}
	}
	i++

	for _, f := range []struct {
		label string
		into  *string
	}{
		{"URI: ", &m.URI},
		{"Version: ", nil},
		{"Chain ID: ", &m.ChainID},
		{"Nonce: ", &m.Nonce},
	} {
		if i >= len(lines) || !strings.HasPrefix(lines[i], f.label) {
			return nil, &ParseError{Field: strings.TrimSuffix(f.label, ": "), Reason: "is missing or out of order"}
		}
		value := strings.TrimPrefix(lines[i], f.label)
		if f.into == nil {
			if value != Version {
				return nil, &ParseError{Field: "Version", Reason: fmt.Sprintf("is %q, and only %q exists", value, Version)}
			}
		} else {
			*f.into = value
		}
		i++
	}

	if m.IssuedAt, i, err = parseTimeLine(lines, i, "Issued At: ", true); err != nil {
		return nil, err
	}
	if m.ExpirationTime, i, err = parseTimeLine(lines, i, "Expiration Time: ", false); err != nil {
		return nil, err
	}
	if m.NotBefore, i, err = parseTimeLine(lines, i, "Not Before: ", false); err != nil {
		return nil, err
	}
	if i < len(lines) && strings.HasPrefix(lines[i], "Request ID: ") {
		m.RequestID = strings.TrimPrefix(lines[i], "Request ID: ")
		i++
	}
	if i < len(lines) && lines[i] == "Resources:" {
		i++
		for ; i < len(lines); i++ {
			if !strings.HasPrefix(lines[i], "- ") {
				return nil, &ParseError{Field: "Resources", Reason: "holds a line that is not a '- ' entry"}
			}
			m.Resources = append(m.Resources, strings.TrimPrefix(lines[i], "- "))
		}
	}
	if i != len(lines) {
		return nil, &ParseError{Field: "message", Reason: fmt.Sprintf("has trailing content: %q", lines[i])}
	}

	if err := m.Validate(); err != nil {
		return nil, err
	}
	return m, nil
}

func parseHeader(line string) (Chain, string, error) {
	if !strings.HasSuffix(line, headerSuffix) {
		return "", "", &ParseError{Field: "header", Reason: "does not end in \" account:\""}
	}
	head := strings.TrimSuffix(line, headerSuffix)
	for chain, word := range chainWord {
		prefix, ok := strings.CutSuffix(head, " wants you to sign in with your "+word)
		if !ok {
			continue
		}
		if prefix == "" {
			return "", "", &ParseError{Field: "domain", Reason: "is empty"}
		}
		return chain, prefix, nil
	}
	return "", "", &ParseError{Field: "header", Reason: "names no chain Orama verifies signatures for"}
}

// parseTimeLine consumes lines[i] when it carries label. required makes its
// absence an error; otherwise the zero time and the same index come back.
func parseTimeLine(lines []string, i int, label string, required bool) (time.Time, int, error) {
	field := strings.TrimSuffix(label, ": ")
	if i >= len(lines) || !strings.HasPrefix(lines[i], label) {
		if required {
			return time.Time{}, i, &ParseError{Field: field, Reason: "is missing or out of order"}
		}
		return time.Time{}, i, nil
	}
	t, err := time.Parse(TimeFormat, strings.TrimPrefix(lines[i], label))
	if err != nil {
		return time.Time{}, i, &ParseError{Field: field, Reason: "is not an RFC 3339 timestamp"}
	}
	return t, i + 1, nil
}

func validateDomain(domain string) error {
	switch {
	case strings.TrimSpace(domain) == "":
		return &ParseError{Field: "domain", Reason: "is empty"}
	case strings.Contains(domain, "://"):
		// The grammar allows "scheme://domain". Orama never issues one, and
		// accepting it here would mean two spellings of the same host that
		// CheckDomain would have to reconcile against a bare Host header.
		return &ParseError{Field: "domain", Reason: "carries a scheme; Orama issues a bare host"}
	case strings.ContainsAny(domain, " \t/"):
		return &ParseError{Field: "domain", Reason: "is not a bare host"}
	}
	return nil
}

// validateAddress checks the address is well-formed for its chain. Whether the
// key behind it made the signature is the verifier's question, not this one's.
func validateAddress(chain Chain, address string) error {
	switch chain {
	case Ethereum:
		if !common.IsHexAddress(address) {
			return &ParseError{Field: "address", Reason: "is not a 20-byte hex address"}
		}
		// EIP-55 mixed-case checksum. It is the only integrity check the
		// address itself carries, and a wallet that renders the message will
		// render the checksummed form, so accepting an unchecksummed one would
		// mean accepting a message no wallet produced.
		if want := common.HexToAddress(address).Hex(); address != want {
			return &ParseError{Field: "address", Reason: "is not EIP-55 checksummed"}
		}
	case Solana:
		if err := validateBase58(address); err != nil {
			return err
		}
		// A 32-byte ed25519 key is 43 or 44 base58 characters; shorter values
		// are valid base58 but cannot be a public key.
		if len(address) < 32 || len(address) > 44 {
			return &ParseError{Field: "address", Reason: "is not the length of a Solana public key"}
		}
	}
	return nil
}

const base58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

func validateBase58(s string) error {
	if s == "" {
		return &ParseError{Field: "address", Reason: "is empty"}
	}
	for _, c := range s {
		if !strings.ContainsRune(base58Alphabet, c) {
			return &ParseError{Field: "address", Reason: "is not base58"}
		}
	}
	return nil
}

func validateURI(uri string) error {
	if strings.TrimSpace(uri) == "" {
		return &ParseError{Field: "URI", Reason: "is empty"}
	}
	u, err := url.Parse(uri)
	if err != nil || !u.IsAbs() {
		return &ParseError{Field: "URI", Reason: "is not an absolute URI"}
	}
	return nil
}

func validateNonce(nonce string) error {
	if len(nonce) < MinNonceLength {
		return &ParseError{Field: "Nonce", Reason: fmt.Sprintf("is shorter than %d characters", MinNonceLength)}
	}
	for _, c := range nonce {
		alphanumeric := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
		if !alphanumeric {
			return &ParseError{Field: "Nonce", Reason: "is not alphanumeric"}
		}
	}
	return nil
}
