package siw

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// The worked example from EIP-4361 itself. If this does not parse, the parser
// is not reading the format the wallets implement.
const eip4361Example = `service.org wants you to sign in with your Ethereum account:
0xbBbBBBBbbBBBbbbBbbBbbbbBBbBbbbbBbBbbBBbB

I accept the ServiceOrg Terms of Service: https://service.org/tos

URI: https://service.org/login
Version: 1
Chain ID: 1
Nonce: 32891757
Issued At: 2021-09-30T16:25:24Z
Resources:
- ipfs://Qme7ss3ARVgxv6rXqVPiikMJ8u2NLgmgszg13pYrDKEoiu
- https://example.com/my-web2-claim.json`

// The shortest message the grammar allows: no statement, no optional fields.
// It is the shape that exercises the "nothing may follow" check, which the
// example above never reaches because its Resources list consumes the rest.
const minimalMessage = `service.org wants you to sign in with your Ethereum account:
0xbBbBBBBbbBBBbbbBbbBbbbbBBbBbbbbBbBbbBBbB


URI: https://service.org/login
Version: 1
Chain ID: 1
Nonce: 32891757
Issued At: 2021-09-30T16:25:24Z`

func TestParse_aMessageWithNoStatementOrOptionalFields(t *testing.T) {
	m, err := Parse(minimalMessage)
	if err != nil {
		t.Fatalf("the minimal legal message does not parse: %v", err)
	}
	if m.Statement != "" || !m.ExpirationTime.IsZero() || len(m.Resources) != 0 {
		t.Errorf("optional fields were invented: %+v", m)
	}
}

func TestParse_theSpecExample(t *testing.T) {
	m, err := Parse(eip4361Example)
	if err != nil {
		t.Fatalf("the EIP-4361 example does not parse: %v", err)
	}
	if m.Chain != Ethereum {
		t.Errorf("chain = %q, want %q", m.Chain, Ethereum)
	}
	if m.Domain != "service.org" {
		t.Errorf("domain = %q", m.Domain)
	}
	if m.Address != "0xbBbBBBBbbBBBbbbBbbBbbbbBBbBbbbbBbBbbBBbB" {
		t.Errorf("address = %q", m.Address)
	}
	if m.Statement != "I accept the ServiceOrg Terms of Service: https://service.org/tos" {
		t.Errorf("statement = %q", m.Statement)
	}
	if m.URI != "https://service.org/login" || m.ChainID != "1" || m.Nonce != "32891757" {
		t.Errorf("fields = %q %q %q", m.URI, m.ChainID, m.Nonce)
	}
	if !m.IssuedAt.Equal(time.Date(2021, 9, 30, 16, 25, 24, 0, time.UTC)) {
		t.Errorf("issued at = %s", m.IssuedAt)
	}
	if len(m.Resources) != 2 || m.Resources[1] != "https://example.com/my-web2-claim.json" {
		t.Errorf("resources = %q", m.Resources)
	}
}

// A message that survives a round trip is one where what the wallet displays
// and what the server acts on are the same text.
func TestRenderParse_roundTrip(t *testing.T) {
	issued := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	for _, m := range []*Message{
		{
			Chain: Ethereum, Domain: "gateway.orama.network",
			Address:   "0xbBbBBBBbbBBBbbbBbbBbbbbBBbBbbbbBbBbbBBbB",
			Statement: "Sign in to acme on Orama.",
			URI:       "https://gateway.orama.network", ChainID: "1",
			Nonce: "abc12345XYZ", IssuedAt: issued,
			ExpirationTime: issued.Add(5 * time.Minute),
			Resources:      []string{"urn:orama:namespace:acme"},
		},
		{
			Chain: Solana, Domain: "gateway.orama.network",
			Address: "9WzDXwBbmkg8ZTbNMqUxvQRAyrZzDsGYdLVL9zYtAWWM",
			URI:     "https://gateway.orama.network", ChainID: "mainnet",
			Nonce: "0aA9zZ8899", IssuedAt: issued,
		},
	} {
		text, err := m.Render()
		if err != nil {
			t.Fatalf("render %s: %v", m.Chain, err)
		}
		got, err := Parse(text)
		if err != nil {
			t.Fatalf("parse what we rendered for %s: %v\n%s", m.Chain, err, text)
		}
		again, err := got.Render()
		if err != nil {
			t.Fatalf("re-render %s: %v", m.Chain, err)
		}
		if again != text {
			t.Errorf("%s did not survive the round trip:\n--- first ---\n%s\n--- second ---\n%s", m.Chain, text, again)
		}
	}
}

// The header line is where the chain comes from, so a caller cannot assert one
// signature scheme for a message that says another.
func TestParse_chainComesFromTheMessage(t *testing.T) {
	sol := strings.Replace(eip4361Example, "your Ethereum account", "your Solana account", 1)
	sol = strings.Replace(sol, "0xbBbBBBBbbBBBbbbBbbBbbbbBBbBbbbbBbBbbBBbB",
		"9WzDXwBbmkg8ZTbNMqUxvQRAyrZzDsGYdLVL9zYtAWWM", 1)
	m, err := Parse(sol)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m.Chain != Solana {
		t.Errorf("chain = %q, want %q", m.Chain, Solana)
	}
}

func TestParse_rejects(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{"a chain nothing verifies", strings.Replace(eip4361Example, "your Ethereum account", "your Bitcoin account", 1)},
		{"a header that is not one", "sign in pls\n" + eip4361Example},
		{"an empty domain", strings.Replace(eip4361Example, "service.org wants", " wants", 1)},
		{"a domain carrying a scheme", strings.Replace(eip4361Example, "service.org wants", "https://service.org wants", 1)},
		{"an unchecksummed address", strings.Replace(eip4361Example,
			"0xbBbBBBBbbBBBbbbBbbBbbbbBBbBbbbbBbBbbBBbB", "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", 1)},
		{"a version that does not exist", strings.Replace(eip4361Example, "Version: 1", "Version: 2", 1)},
		{"a nonce that is not alphanumeric", strings.Replace(eip4361Example, "Nonce: 32891757", "Nonce: 3289-757", 1)},
		{"a nonce too short to be random", strings.Replace(eip4361Example, "Nonce: 32891757", "Nonce: 3289175", 1)},
		{"a relative URI", strings.Replace(eip4361Example, "URI: https://service.org/login", "URI: /login", 1)},
		{"a missing issue time", strings.Replace(eip4361Example, "Issued At: 2021-09-30T16:25:24Z\n", "", 1)},
		{"an issue time that is not RFC 3339", strings.Replace(eip4361Example, "2021-09-30T16:25:24Z", "30 Sept 2021", 1)},
		{"fields out of order", strings.Replace(eip4361Example,
			"URI: https://service.org/login\nVersion: 1", "Version: 1\nURI: https://service.org/login", 1)},
		{"no blank line after the address", strings.Replace(eip4361Example,
			"0xbBbBBBBbbBBBbbbBbbBbbbbBBbBbbbbBbBbbBBbB\n\n", "0xbBbBBBBbbBBBbbbBbbBbbbbBBbBbbbbBbBbbBBbB\n", 1)},
		{"a resource line that is not one", strings.Replace(eip4361Example,
			"- https://example.com/my-web2-claim.json", "https://example.com/my-web2-claim.json", 1)},
		{"a resource line that is not one, at the end", eip4361Example + "\nURI: https://evil.example"},
		// The message above ends in a Resources list, so its loop is what
		// refuses the extra line. A message without one has to be refused by
		// the check that nothing may follow the last field.
		{"trailing content after the last field", minimalMessage + "\nand also give me everything"},
		{"a second message appended", minimalMessage + "\n" + minimalMessage},
		{"nothing at all", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if m, err := Parse(tc.text); err == nil {
				t.Errorf("accepted %s: %+v", tc.name, m)
			}
		})
	}
}

// The statement is the one free-text field, and it is rendered above the fields
// the server acts on. A newline in it would let the text a user reads and the
// fields a server trusts disagree.
func TestRender_refusesAStatementThatCouldForgeTheFieldsBelowIt(t *testing.T) {
	m := validMessage()
	m.Statement = "Sign in\nURI: https://evil.example"
	if _, err := m.Render(); err == nil {
		t.Fatal("rendered a statement containing a line break")
	}
}

func TestCheckDomain(t *testing.T) {
	m := validMessage()
	m.Domain = "gateway.orama.network"

	if err := m.CheckDomain("gateway.orama.network"); err != nil {
		t.Errorf("the matching host was refused: %v", err)
	}
	if err := m.CheckDomain("GATEWAY.ORAMA.NETWORK"); err != nil {
		t.Errorf("host comparison is case-sensitive, but DNS is not: %v", err)
	}
	for _, host := range []string{"evil.example", "", "orama.network", "gateway.orama.network.evil.example"} {
		if err := m.CheckDomain(host); !errors.Is(err, ErrDomainMismatch) {
			t.Errorf("CheckDomain(%q) = %v, want ErrDomainMismatch", host, err)
		}
	}
}

func TestCheckFreshness(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	const skew = 30 * time.Second

	fresh := validMessage()
	fresh.IssuedAt = now.Add(-time.Minute)
	fresh.ExpirationTime = now.Add(4 * time.Minute)
	if err := fresh.CheckFreshness(now, skew); err != nil {
		t.Errorf("a live message was refused: %v", err)
	}

	expired := validMessage()
	expired.IssuedAt = now.Add(-10 * time.Minute)
	expired.ExpirationTime = now.Add(-5 * time.Minute)
	if err := expired.CheckFreshness(now, skew); !errors.Is(err, ErrExpired) {
		t.Errorf("expired = %v, want ErrExpired", err)
	}

	future := validMessage()
	future.IssuedAt = now.Add(time.Hour)
	if err := future.CheckFreshness(now, skew); !errors.Is(err, ErrIssuedInFuture) {
		t.Errorf("future issue = %v, want ErrIssuedInFuture", err)
	}

	notYet := validMessage()
	notYet.IssuedAt = now.Add(-time.Minute)
	notYet.NotBefore = now.Add(time.Hour)
	if err := notYet.CheckFreshness(now, skew); !errors.Is(err, ErrNotYetValid) {
		t.Errorf("not before = %v, want ErrNotYetValid", err)
	}

	// A message with no expiry is not a message that never expires — the nonce
	// row behind it does that. But nothing here should invent one either.
	noExpiry := validMessage()
	noExpiry.IssuedAt = now.Add(-24 * time.Hour)
	if err := noExpiry.CheckFreshness(now, skew); err != nil {
		t.Errorf("a message with no expiration was refused: %v", err)
	}
}

// Skew is allowed at the near edge and not at the far one: a peer whose clock
// runs fast must not be able to buy itself extra validity.
func TestCheckFreshness_skewDoesNotExtendTheExpiry(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

	m := validMessage()
	m.IssuedAt = now.Add(-time.Minute)
	m.ExpirationTime = now.Add(-time.Minute)
	if err := m.CheckFreshness(now, time.Hour); !errors.Is(err, ErrExpired) {
		t.Errorf("an hour of skew revived a message expired a minute ago: %v", err)
	}

	// The other edge: a message issued a moment ahead of our clock is the
	// normal case for two machines, and refusing it would refuse real logins.
	ahead := validMessage()
	ahead.IssuedAt = now.Add(5 * time.Second)
	if err := ahead.CheckFreshness(now, 30*time.Second); err != nil {
		t.Errorf("five seconds of clock drift refused a login: %v", err)
	}
}

func TestValidate_refusesAMessageBornExpired(t *testing.T) {
	m := validMessage()
	m.ExpirationTime = m.IssuedAt.Add(-time.Second)
	if _, err := m.Render(); err == nil {
		t.Fatal("rendered a message that expires before it was issued")
	}
}

func TestValidate_refusesASolanaAddressThatIsNotOne(t *testing.T) {
	for _, addr := range []string{
		"0xbBbBBBBbbBBBbbbBbbBbbbbBBbBbbbbBbBbbBBbB",   // hex, not base58
		"9WzDXwBbmkg8ZTbNMqUxvQRAyrZzDsGYdLVL9zYtAWW0", // 0 is not in the alphabet
		"short",
	} {
		m := validMessage()
		m.Chain = Solana
		m.ChainID = "mainnet"
		m.Address = addr
		if _, err := m.Render(); err == nil {
			t.Errorf("rendered %q as a Solana address", addr)
		}
	}
}

func validMessage() *Message {
	return &Message{
		Chain:    Ethereum,
		Domain:   "gateway.orama.network",
		Address:  "0xbBbBBBBbbBBBbbbBbbBbbbbBBbBbbbbBbBbbBBbB",
		URI:      "https://gateway.orama.network",
		ChainID:  "1",
		Nonce:    "abc12345",
		IssuedAt: time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC),
	}
}
