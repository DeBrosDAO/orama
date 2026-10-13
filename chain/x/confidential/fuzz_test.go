package confidential

import (
	"bytes"
	"errors"
	"testing"
)

// FuzzConfidentialQuote: no quote, however well formed, is ever accepted. ParseQuote never panics
// and reads only the lengths the layout fixes; Accept refuses every report; the default verifier
// and a verifier over an empty registry never return a verified report; and RegisterNode never
// returns an attested node, with the default verifier or with none.
func FuzzConfidentialQuote(f *testing.F) {
	snp := make([]byte, snpReportSize)
	snp[snpVersionOffset] = 2
	snp[snpSigAlgoOffset] = snpSigAlgoECDSAP384
	f.Add(snp, []byte("measurement"), "operator", "node-1", []byte("key"))
	tdx := make([]byte, tdxSigLenOffset+4+tdxMinSigDataSize)
	tdx[0], tdx[2], tdx[4] = tdxQuoteVersion, tdxAttKeyP256, tdxTEEType
	tdx[tdxSigLenOffset] = tdxMinSigDataSize
	f.Add(tdx, []byte("measurement"), "operator", "node-1", []byte("key"))
	f.Add([]byte{}, []byte{}, "", "", []byte{})
	f.Fuzz(func(t *testing.T, quote, measurement []byte, operator, nodeID string, nodeKey []byte) {
		info, err := ParseQuote(quote)
		if err == nil {
			if len(info.Measurement) != measurementSizeSNP || len(info.ReportData) != reportDataSize {
				t.Fatalf("parsed %s quote with %d-byte measurement and %d-byte report data", info.Kind, len(info.Measurement), len(info.ReportData))
			}
			if info.Kind != KindSEVSNP && info.Kind != KindTDX {
				t.Fatalf("unknown TEE kind %q", info.Kind)
			}
		} else if !errors.Is(err, ErrMalformedQuote) {
			t.Fatalf("a refused quote is not ErrMalformedQuote: %v", err)
		}
		if Accept(quote) == nil {
			t.Fatal("Accept accepted a report")
		}
		for _, v := range []AttestationVerifier{DefaultVerifier(), NewQuoteVerifier(EmptyRoots())} {
			report, err := v.Verify(quote, measurement)
			if err == nil || report.IsVerified() {
				t.Fatalf("a verifier verified a quote (err=%v)", err)
			}
		}
		node, err := RegisterNode(DefaultVerifier(), NodeRegistration{Operator: operator, NodeID: nodeID, Quote: quote, ExpectedMeasurement: measurement, NodeKey: nodeKey})
		if err == nil || node.Attested() {
			t.Fatalf("a node was registered with the default verifier (err=%v)", err)
		}
		if node, err := RegisterNode(nil, NodeRegistration{Operator: operator, NodeID: nodeID, Quote: quote, NodeKey: nodeKey}); err == nil || node.Attested() {
			t.Fatal("a node was registered with no verifier")
		}
	})
}

// FuzzConfidentialReportData: the report data a node must carry binds the operator, the node id and
// the key unambiguously: two different triples never give the same 64 bytes, so a quote made for
// one registration cannot be replayed for another.
func FuzzConfidentialReportData(f *testing.F) {
	f.Add("ab", "c", []byte("k"), "a", "bc", []byte("k"))
	f.Add("", "", []byte{}, "", "", []byte{1})
	f.Fuzz(func(t *testing.T, op1, id1 string, key1 []byte, op2, id2 string, key2 []byte) {
		r1, r2 := ReportDataFor(op1, id1, key1), ReportDataFor(op2, id2, key2)
		same := op1 == op2 && id1 == id2 && bytes.Equal(key1, key2)
		if len(r1) != reportDataSize || same != bytes.Equal(r1, r2) {
			t.Fatalf("equal triples=%v but equal report data=%v (len %d)", same, bytes.Equal(r1, r2), len(r1))
		}
	})
}

// FuzzConfidentialRoots: NewRootRegistry takes arbitrary DER and never panics. A registry is built
// only from a self-signed CA certificate of a known TEE kind, and an arbitrary byte string is never
// one; the empty registry accepts no vendor.
func FuzzConfidentialRoots(f *testing.F) {
	f.Add([]byte{0x30, 0x03, 0x02, 0x01, 0x00}, true)
	f.Add([]byte{}, false)
	f.Fuzz(func(t *testing.T, der []byte, snp bool) {
		kind := KindTDX
		if snp {
			kind = KindSEVSNP
		}
		reg, err := NewRootRegistry(VendorRoot{Kind: kind, DER: der})
		if err == nil {
			if reg.Len() != 1 || !reg.Has(kind) {
				t.Fatalf("registry holds %d roots after one accepted", reg.Len())
			}
		}
		if EmptyRoots().Has(KindSEVSNP) || EmptyRoots().Has(KindTDX) || EmptyRoots().Len() != 0 {
			t.Fatal("the default registry holds a vendor")
		}
		if _, err := NewRootRegistry(VendorRoot{Kind: "unknown", DER: der}); err == nil {
			t.Fatal("a root of an unknown TEE kind was accepted")
		}
	})
}
