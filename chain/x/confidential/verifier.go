package confidential

import (
	"errors"
	"fmt"
)

// TEEKind names a trusted-execution technology.
type TEEKind string

const (
	// KindSEVSNP is AMD SEV-SNP.
	KindSEVSNP TEEKind = "SEV_SNP"
	// KindTDX is Intel TDX.
	KindTDX TEEKind = "TDX"
)

var (
	// ErrNoVerifier is returned when no verifier is supplied.
	ErrNoVerifier = errors.New("no attestation verifier is linked")
	// ErrMalformedQuote is returned for a quote that is not a well-formed SEV-SNP report or TDX quote.
	ErrMalformedQuote = errors.New("quote is not a well-formed SEV-SNP report or TDX quote")
	// ErrNoVendorRoot is returned when the registry holds no accepted vendor root for the quote's TEE.
	ErrNoVendorRoot = errors.New("no accepted vendor root for this TEE: the registry is empty by default")
	// ErrVerifierNotLinked is returned when a vendor root exists but no certificate-chain and signature
	// verification is linked. Nothing in this package can prove a quote genuine.
	ErrVerifierNotLinked = errors.New("quote signature and certificate chain cannot be verified: no verifier is linked")
	// ErrUnverifiedReport is returned when a report was not produced by a verifier.
	ErrUnverifiedReport = errors.New("report is not verified")
	// ErrMeasurementMismatch is returned when a verified report's measurement is not the expected one.
	ErrMeasurementMismatch = errors.New("report measurement does not match the expected measurement")
	// ErrReportDataMismatch is returned when a verified report is not bound to the node it registers.
	ErrReportDataMismatch = errors.New("report data does not bind this operator, node id and node key")
)

// VerifiedReport is a report a verifier has proven genuine: signed by a key
// that chains to an accepted vendor root. The zero value is not verified, and
// code outside this package cannot make one that is.
type VerifiedReport struct {
	Kind        TEEKind
	Measurement []byte
	ReportData  []byte
	PlatformID  []byte

	verified bool
}

// IsVerified reports whether a verifier produced r.
func (r VerifiedReport) IsVerified() bool { return r.verified }

// AttestationVerifier turns quote bytes into a verified report. It must return
// a verified report only when the quote's signature chains to an accepted
// vendor root and its measurement equals expectedMeasurement. Every other
// outcome is an error.
type AttestationVerifier interface {
	Verify(quote, expectedMeasurement []byte) (VerifiedReport, error)
}

// DefaultVerifier is the verifier this binary uses: the SEV-SNP and TDX
// checks over the default (empty) vendor root registry. It refuses every quote.
func DefaultVerifier() AttestationVerifier {
	return NewQuoteVerifier(EmptyRoots())
}

// QuoteVerifier parses a quote, then requires an accepted vendor root for its
// TEE. Certificate-chain and signature verification is not implemented, so it
// refuses even when a root is present.
type QuoteVerifier struct {
	roots RootRegistry
}

// NewQuoteVerifier returns a QuoteVerifier over roots.
func NewQuoteVerifier(roots RootRegistry) QuoteVerifier { return QuoteVerifier{roots: roots} }

// Verify always returns an error.
func (v QuoteVerifier) Verify(quote, expectedMeasurement []byte) (VerifiedReport, error) {
	if len(expectedMeasurement) == 0 {
		return VerifiedReport{}, fmt.Errorf("expected measurement is empty: %w", ErrMalformedQuote)
	}
	info, err := ParseQuote(quote)
	if err != nil {
		return VerifiedReport{}, err
	}
	if !v.roots.Has(info.Kind) {
		return VerifiedReport{}, fmt.Errorf("%s quote: %w", info.Kind, ErrNoVendorRoot)
	}
	return VerifiedReport{}, fmt.Errorf("%s quote: %w", info.Kind, ErrVerifierNotLinked)
}
