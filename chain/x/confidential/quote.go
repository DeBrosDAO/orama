package confidential

import (
	"encoding/binary"
	"fmt"
)

// SEV-SNP attestation report layout (AMD SEV-SNP ABI, report structure v2/v3/v5).
const (
	snpReportSize        = 0x4A0
	snpVersionOffset     = 0x00
	snpSigAlgoOffset     = 0x34
	snpReportDataOffset  = 0x50
	snpMeasurementOffset = 0x90
	snpChipIDOffset      = 0x1A0
	snpSignatureOffset   = 0x2A0

	snpSigAlgoECDSAP384 = 1

	measurementSizeSNP = 48
	reportDataSize     = 64
	chipIDSize         = 64
)

// TDX quote v4 layout (Intel TDX DCAP quote, ECDSA-256 attestation key).
const (
	tdxHeaderSize     = 48
	tdxBodySize       = 584
	tdxMRTDOffset     = tdxHeaderSize + 136
	tdxReportData     = tdxHeaderSize + 520
	tdxSigLenOffset   = tdxHeaderSize + tdxBodySize
	tdxMinSigDataSize = 64 + 64 // ECDSA signature and attestation public key

	tdxQuoteVersion = 4
	tdxTEEType      = 0x81
	tdxAttKeyP256   = 2

	measurementSizeTDX = 48
)

// QuoteInfo is what a quote claims about itself. None of it is verified: an
// attacker can write any of these bytes.
type QuoteInfo struct {
	Kind        TEEKind
	Measurement []byte
	ReportData  []byte
	PlatformID  []byte
}

// ParseQuote reads the fields a SEV-SNP report or TDX quote claims, and checks
// only that it is structurally well-formed. It verifies no signature, so its
// result is never evidence of anything.
func ParseQuote(quote []byte) (QuoteInfo, error) {
	if len(quote) == 0 {
		return QuoteInfo{}, fmt.Errorf("empty quote: %w", ErrMalformedQuote)
	}
	if len(quote) == snpReportSize && isSNPVersion(binary.LittleEndian.Uint32(quote[snpVersionOffset:])) {
		return parseSNP(quote)
	}
	if len(quote) > tdxSigLenOffset+4 {
		return parseTDX(quote)
	}
	return QuoteInfo{}, fmt.Errorf("%d bytes is neither a %d-byte SEV-SNP report nor a TDX quote: %w", len(quote), snpReportSize, ErrMalformedQuote)
}

func isSNPVersion(v uint32) bool { return v == 2 || v == 3 || v == 5 }

func parseSNP(quote []byte) (QuoteInfo, error) {
	if algo := binary.LittleEndian.Uint32(quote[snpSigAlgoOffset:]); algo != snpSigAlgoECDSAP384 {
		return QuoteInfo{}, fmt.Errorf("SEV-SNP signature algorithm %d: %w", algo, ErrMalformedQuote)
	}
	return QuoteInfo{
		Kind:        KindSEVSNP,
		Measurement: append([]byte(nil), quote[snpMeasurementOffset:snpMeasurementOffset+measurementSizeSNP]...),
		ReportData:  append([]byte(nil), quote[snpReportDataOffset:snpReportDataOffset+reportDataSize]...),
		PlatformID:  append([]byte(nil), quote[snpChipIDOffset:snpChipIDOffset+chipIDSize]...),
	}, nil
}

func parseTDX(quote []byte) (QuoteInfo, error) {
	if v := binary.LittleEndian.Uint16(quote[0:]); v != tdxQuoteVersion {
		return QuoteInfo{}, fmt.Errorf("TDX quote version %d: %w", v, ErrMalformedQuote)
	}
	if k := binary.LittleEndian.Uint16(quote[2:]); k != tdxAttKeyP256 {
		return QuoteInfo{}, fmt.Errorf("TDX attestation key type %d: %w", k, ErrMalformedQuote)
	}
	if t := binary.LittleEndian.Uint32(quote[4:]); t != tdxTEEType {
		return QuoteInfo{}, fmt.Errorf("TEE type %#x is not TDX: %w", t, ErrMalformedQuote)
	}
	sigLen := binary.LittleEndian.Uint32(quote[tdxSigLenOffset:])
	rest := len(quote) - tdxSigLenOffset - 4
	if sigLen < tdxMinSigDataSize || int64(sigLen) != int64(rest) {
		return QuoteInfo{}, fmt.Errorf("TDX signature data length %d does not match the %d bytes present: %w", sigLen, rest, ErrMalformedQuote)
	}
	return QuoteInfo{
		Kind:        KindTDX,
		Measurement: append([]byte(nil), quote[tdxMRTDOffset:tdxMRTDOffset+measurementSizeTDX]...),
		ReportData:  append([]byte(nil), quote[tdxReportData:tdxReportData+reportDataSize]...),
	}, nil
}
