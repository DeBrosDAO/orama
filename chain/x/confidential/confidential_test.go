package confidential

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// unsignedSNPReport is a structurally valid SEV-SNP report whose signature is
// all zero: nothing signed it.
func unsignedSNPReport(measurement []byte) []byte {
	r := make([]byte, snpReportSize)
	binary.LittleEndian.PutUint32(r[snpVersionOffset:], 2)
	binary.LittleEndian.PutUint32(r[snpSigAlgoOffset:], snpSigAlgoECDSAP384)
	copy(r[snpMeasurementOffset:], measurement)
	return r
}

// bogusSigTDXQuote is a structurally valid TDX v4 quote whose "signature" is random bytes.
func bogusSigTDXQuote(t *testing.T, measurement []byte) []byte {
	t.Helper()
	q := make([]byte, tdxSigLenOffset+4+tdxMinSigDataSize)
	binary.LittleEndian.PutUint16(q[0:], tdxQuoteVersion)
	binary.LittleEndian.PutUint16(q[2:], tdxAttKeyP256)
	binary.LittleEndian.PutUint32(q[4:], tdxTEEType)
	copy(q[tdxMRTDOffset:], measurement)
	binary.LittleEndian.PutUint32(q[tdxSigLenOffset:], tdxMinSigDataSize)
	if _, err := rand.Read(q[tdxSigLenOffset+4:]); err != nil {
		t.Fatal(err)
	}
	return q
}

func selfSignedRoot(t *testing.T, kind TEEKind, isCA bool) VendorRoot {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: "test vendor root, not a real vendor"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  isCA,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return VendorRoot{Kind: kind, DER: der}
}

var wantMeasurement = bytes.Repeat([]byte{0xAB}, 48)

func TestAccept_refusesEveryReport(t *testing.T) {
	for _, report := range [][]byte{nil, {}, []byte("sev-snp"), []byte("tdx"), unsignedSNPReport(wantMeasurement)} {
		if err := Accept(report); !errors.Is(err, ErrNoAttestation) {
			t.Fatalf("report %q: %v", report, err)
		}
	}
}

func TestDefaultVerifier_refusesEveryQuote(t *testing.T) {
	cases := []struct {
		name    string
		quote   []byte
		wantErr error
	}{
		{"nil", nil, ErrMalformedQuote},
		{"empty", []byte{}, ErrMalformedQuote},
		{"garbage text", []byte("this is not a quote"), ErrMalformedQuote},
		{"garbage of report size", bytes.Repeat([]byte{0xFF}, snpReportSize), ErrMalformedQuote},
		{"unsigned SEV-SNP report", unsignedSNPReport(wantMeasurement), ErrNoVendorRoot},
		{"TDX quote with a bogus signature", bogusSigTDXQuote(t, wantMeasurement), ErrNoVendorRoot},
	}
	v := DefaultVerifier()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report, err := v.Verify(tc.quote, wantMeasurement)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("got %v, want %v", err, tc.wantErr)
			}
			if report.IsVerified() {
				t.Fatal("a refused quote produced a verified report")
			}
		})
	}
}

func TestDefaultVerifier_refusesAnEmptyExpectedMeasurement(t *testing.T) {
	if _, err := DefaultVerifier().Verify(unsignedSNPReport(wantMeasurement), nil); !errors.Is(err, ErrMalformedQuote) {
		t.Fatalf("got %v", err)
	}
}

func TestQuoteVerifier_refusesEvenWithARootBecauseNoChainCheckIsLinked(t *testing.T) {
	snpRoot, tdxRoot := selfSignedRoot(t, KindSEVSNP, true), selfSignedRoot(t, KindTDX, true)
	roots, err := NewRootRegistry(snpRoot, tdxRoot)
	if err != nil {
		t.Fatal(err)
	}
	v := NewQuoteVerifier(roots)
	for name, quote := range map[string][]byte{
		"unsigned SEV-SNP": unsignedSNPReport(wantMeasurement),
		"bogus-sig TDX":    bogusSigTDXQuote(t, wantMeasurement),
	} {
		report, err := v.Verify(quote, wantMeasurement)
		if !errors.Is(err, ErrVerifierNotLinked) || report.IsVerified() {
			t.Fatalf("%s: %v verified=%v", name, err, report.IsVerified())
		}
	}
}

func TestQuoteVerifier_aRootForTheOtherTEEDoesNotHelp(t *testing.T) {
	roots, err := NewRootRegistry(selfSignedRoot(t, KindTDX, true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewQuoteVerifier(roots).Verify(unsignedSNPReport(wantMeasurement), wantMeasurement); !errors.Is(err, ErrNoVendorRoot) {
		t.Fatalf("got %v", err)
	}
}

func TestRootRegistry_emptyByDefaultAndValidatesRoots(t *testing.T) {
	if EmptyRoots().Len() != 0 || EmptyRoots().Has(KindSEVSNP) || EmptyRoots().Has(KindTDX) {
		t.Fatal("the default registry must accept no vendor")
	}
	good := selfSignedRoot(t, KindSEVSNP, true)
	cases := map[string][]VendorRoot{
		"unknown kind":     {{Kind: "ARM_CCA", DER: good.DER}},
		"not a cert":       {{Kind: KindTDX, DER: []byte("not der")}},
		"not a CA":         {selfSignedRoot(t, KindTDX, false)},
		"duplicate":        {good, good},
		"empty der":        {{Kind: KindTDX}},
		"truncated cert":   {{Kind: KindTDX, DER: good.DER[:len(good.DER)/2]}},
		"one bad of two":   {good, {Kind: KindTDX, DER: []byte{1, 2, 3}}},
		"kind is required": {{DER: good.DER}},
	}
	for name, roots := range cases {
		if _, err := NewRootRegistry(roots...); err == nil {
			t.Fatalf("%s: want an error", name)
		}
	}
	reg, err := NewRootRegistry(good)
	if err != nil || reg.Len() != 1 || !reg.Has(KindSEVSNP) || reg.Has(KindTDX) {
		t.Fatalf("a valid root: %v len=%d", err, reg.Len())
	}
	if _, err := NewRootRegistry(); err != nil {
		t.Fatalf("no roots is the default: %v", err)
	}
}

func TestParseQuote_readsClaimsButProvesNothing(t *testing.T) {
	info, err := ParseQuote(unsignedSNPReport(wantMeasurement))
	if err != nil || info.Kind != KindSEVSNP || !bytes.Equal(info.Measurement, wantMeasurement) {
		t.Fatalf("SEV-SNP: %v %+v", err, info)
	}
	info, err = ParseQuote(bogusSigTDXQuote(t, wantMeasurement))
	if err != nil || info.Kind != KindTDX || !bytes.Equal(info.Measurement, wantMeasurement) {
		t.Fatalf("TDX: %v %+v", err, info)
	}
}

func TestParseQuote_rejectsMalformedQuotes(t *testing.T) {
	badVersion := unsignedSNPReport(wantMeasurement)
	binary.LittleEndian.PutUint32(badVersion[snpVersionOffset:], 9)
	badAlgo := unsignedSNPReport(wantMeasurement)
	binary.LittleEndian.PutUint32(badAlgo[snpSigAlgoOffset:], 7)
	tdx := bogusSigTDXQuote(t, wantMeasurement)
	wrongTEE := append([]byte(nil), tdx...)
	binary.LittleEndian.PutUint32(wrongTEE[4:], 0)
	wrongVersion := append([]byte(nil), tdx...)
	binary.LittleEndian.PutUint16(wrongVersion[0:], 3)
	wrongKey := append([]byte(nil), tdx...)
	binary.LittleEndian.PutUint16(wrongKey[2:], 3)
	shortSig := append([]byte(nil), tdx...)
	binary.LittleEndian.PutUint32(shortSig[tdxSigLenOffset:], 8)
	lenMismatch := append(append([]byte(nil), tdx...), 0)

	cases := map[string][]byte{
		"nil": nil, "short": {1, 2, 3},
		"SNP version": badVersion, "SNP algorithm": badAlgo,
		"TDX wrong TEE type": wrongTEE, "TDX version": wrongVersion, "TDX key type": wrongKey,
		"TDX short signature": shortSig, "TDX length mismatch": lenMismatch,
	}
	for name, quote := range cases {
		if _, err := ParseQuote(quote); !errors.Is(err, ErrMalformedQuote) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

// stubVerifier stands in for a verifier that succeeded, so the registration and
// listing checks after verification can be exercised. It exists only in tests: no
// production code returns a verified report, and a stub report is not a quote.
type stubVerifier struct{ report VerifiedReport }

func (s stubVerifier) Verify(_, _ []byte) (VerifiedReport, error) { return s.report, nil }

func stubReport(operator, nodeID string, key, measurement []byte) VerifiedReport {
	return VerifiedReport{
		Kind: KindSEVSNP, Measurement: measurement,
		ReportData: ReportDataFor(operator, nodeID, key), verified: true,
	}
}

func registration() NodeRegistration {
	return NodeRegistration{
		Operator: "orama1operator", NodeID: "node-1", Quote: unsignedSNPReport(wantMeasurement),
		ExpectedMeasurement: wantMeasurement, NodeKey: bytes.Repeat([]byte{7}, 32),
	}
}

func TestRegisterNode_refusesEveryQuoteWithTheDefaultVerifier(t *testing.T) {
	for name, quote := range map[string][]byte{
		"nil": nil, "empty": {}, "garbage": []byte("garbage"),
		"unsigned SEV-SNP": unsignedSNPReport(wantMeasurement),
		"bogus-sig TDX":    bogusSigTDXQuote(t, wantMeasurement),
	} {
		reg := registration()
		reg.Quote = quote
		node, err := RegisterNode(DefaultVerifier(), reg)
		if err == nil || node.Attested() {
			t.Fatalf("%s: registered a node without a verifier (%v)", name, err)
		}
	}
}

func TestRegisterNode_refusesBadInputAndANilVerifier(t *testing.T) {
	if _, err := RegisterNode(nil, registration()); !errors.Is(err, ErrNoVerifier) {
		t.Fatalf("nil verifier: %v", err)
	}
	for name, mutate := range map[string]func(*NodeRegistration){
		"no operator": func(r *NodeRegistration) { r.Operator = "" },
		"no node id":  func(r *NodeRegistration) { r.NodeID = "" },
		"no key":      func(r *NodeRegistration) { r.NodeKey = nil },
	} {
		reg := registration()
		mutate(&reg)
		if _, err := RegisterNode(stubVerifier{}, reg); err == nil {
			t.Fatalf("%s: want an error", name)
		}
	}
}

func TestRegisterNode_checksAfterVerification(t *testing.T) {
	reg := registration()
	good := stubReport(reg.Operator, reg.NodeID, reg.NodeKey, wantMeasurement)

	unverified := good
	unverified.verified = false
	wrongMeasurement := stubReport(reg.Operator, reg.NodeID, reg.NodeKey, bytes.Repeat([]byte{1}, 48))
	otherNode := stubReport(reg.Operator, "node-2", reg.NodeKey, wantMeasurement)
	otherKey := stubReport(reg.Operator, reg.NodeID, bytes.Repeat([]byte{8}, 32), wantMeasurement)

	cases := []struct {
		name    string
		report  VerifiedReport
		wantErr error
	}{
		{"zero report", VerifiedReport{}, ErrUnverifiedReport},
		{"unverified report", unverified, ErrUnverifiedReport},
		{"wrong measurement", wrongMeasurement, ErrMeasurementMismatch},
		{"bound to another node id", otherNode, ErrReportDataMismatch},
		{"bound to another key", otherKey, ErrReportDataMismatch},
	}
	for _, tc := range cases {
		if _, err := RegisterNode(stubVerifier{report: tc.report}, reg); !errors.Is(err, tc.wantErr) {
			t.Fatalf("%s: got %v, want %v", tc.name, err, tc.wantErr)
		}
	}

	node, err := RegisterNode(stubVerifier{report: good}, reg)
	if err != nil || !node.Attested() || node.NodeID != "node-1" {
		t.Fatalf("a verified, bound report registers: %v %+v", err, node)
	}
}

func TestReportDataFor_isUnambiguous(t *testing.T) {
	a := ReportDataFor("op", "ab", []byte("c"))
	b := ReportDataFor("op", "a", []byte("bc"))
	if bytes.Equal(a, b) || len(a) != reportDataSize {
		t.Fatal("field boundaries must be part of the binding")
	}
}

func TestListing_needsAnAttestedNodeAndCannotBeLeased(t *testing.T) {
	terms := ListingTerms{CPUMillis: 1000, MemoryMiB: 1024, PriceNoramaPerHour: 5}
	if _, err := NewListing(Node{}, terms); !errors.Is(err, ErrUnverifiedReport) {
		t.Fatalf("an unattested node: %v", err)
	}
	if _, err := NewListing(Node{Operator: "o", NodeID: "n", Report: VerifiedReport{Measurement: wantMeasurement}}, terms); !errors.Is(err, ErrUnverifiedReport) {
		t.Fatalf("a hand-built report: %v", err)
	}

	reg := registration()
	node, err := RegisterNode(stubVerifier{report: stubReport(reg.Operator, reg.NodeID, reg.NodeKey, wantMeasurement)}, reg)
	if err != nil {
		t.Fatal(err)
	}
	for name, bad := range map[string]ListingTerms{
		"no cpu": {MemoryMiB: 1, PriceNoramaPerHour: 1}, "no memory": {CPUMillis: 1, PriceNoramaPerHour: 1}, "no price": {CPUMillis: 1, MemoryMiB: 1},
	} {
		if _, err := NewListing(node, bad); err == nil {
			t.Fatalf("%s: want an error", name)
		}
	}
	listing, err := NewListing(node, terms)
	if err != nil {
		t.Fatal(err)
	}
	if MarketplaceLive {
		t.Fatal("the marketplace must not be live")
	}
	if err := listing.Lease("orama1tenant"); !errors.Is(err, ErrMarketplaceNotLive) {
		t.Fatalf("even an attested listing cannot be leased: %v", err)
	}
}

// TestNothingImportsConfidential keeps the package unwired: no other package in the chain
// module imports it, so no message or module path can reach it. Wiring it is a deliberate
// change that must replace this test with the real verifier's.
func TestNothingImportsConfidential(t *testing.T) {
	root := filepath.Join("..", "..")
	const self = "x/confidential"
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.Contains(filepath.ToSlash(path), self) {
			return err
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(src), "chain/"+self) {
			t.Errorf("%s imports %s", path, self)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
