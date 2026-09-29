package confidential

import (
	"bytes"
	"crypto/sha512"
	"fmt"
)

// bindDomain separates the report-data binding from every other hash.
const bindDomain = "orama-confidential-bind-v1"

// NodeRegistration is what an operator submits to register a confidential
// node. It carries no trust: RegisterNode accepts it only if a verifier proves
// the quote.
type NodeRegistration struct {
	Operator string
	NodeID   string
	// Quote is the SEV-SNP report or TDX quote.
	Quote []byte
	// ExpectedMeasurement is the launch measurement of the published OramaOS build.
	ExpectedMeasurement []byte
	// NodeKey is the public key the quote's report data binds.
	NodeKey []byte
}

// ReportDataFor is the 64-byte report data a node must place in its quote:
// SHA-512 over the domain, operator, node id and node key.
func ReportDataFor(operator, nodeID string, nodeKey []byte) []byte {
	h := sha512.New()
	for _, part := range [][]byte{[]byte(bindDomain), []byte(operator), []byte(nodeID), nodeKey} {
		fmt.Fprintf(h, "%d|", len(part))
		h.Write(part)
	}
	return h.Sum(nil)
}

// Node is a registered confidential node. It carries the verified report that
// admitted it; RegisterNode is the only constructor that fills one in.
type Node struct {
	Operator string
	NodeID   string
	Report   VerifiedReport
}

// Attested reports whether the node holds a verified report.
func (n Node) Attested() bool { return n.Report.IsVerified() }

// RegisterNode admits a confidential node only when v proves its quote. Any
// other outcome, including a nil verifier, is an error and returns no node.
func RegisterNode(v AttestationVerifier, reg NodeRegistration) (Node, error) {
	if reg.Operator == "" || reg.NodeID == "" {
		return Node{}, fmt.Errorf("operator and node id are required")
	}
	if len(reg.NodeKey) == 0 {
		return Node{}, fmt.Errorf("node key is required")
	}
	if v == nil {
		return Node{}, ErrNoVerifier
	}
	report, err := v.Verify(reg.Quote, reg.ExpectedMeasurement)
	if err != nil {
		return Node{}, fmt.Errorf("node %s of operator %s: %w", reg.NodeID, reg.Operator, err)
	}
	if !report.IsVerified() {
		return Node{}, ErrUnverifiedReport
	}
	if !bytes.Equal(report.Measurement, reg.ExpectedMeasurement) {
		return Node{}, ErrMeasurementMismatch
	}
	if !bytes.Equal(report.ReportData, ReportDataFor(reg.Operator, reg.NodeID, reg.NodeKey)) {
		return Node{}, ErrReportDataMismatch
	}
	return Node{Operator: reg.Operator, NodeID: reg.NodeID, Report: report}, nil
}
