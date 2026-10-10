package confidential

import (
	"crypto/sha256"
	"crypto/x509"
	"fmt"
)

// VendorRoot is a vendor root certificate a quote's chain must end at: AMD's ARK
// for SEV-SNP, Intel's SGX root CA for TDX.
type VendorRoot struct {
	Kind TEEKind
	// DER is the root certificate.
	DER []byte
}

// Fingerprint is the SHA-256 of the root's DER.
func (r VendorRoot) Fingerprint() [32]byte { return sha256.Sum256(r.DER) }

// RootRegistry holds the vendor roots this chain accepts. The default registry
// is empty, so no quote can chain to anything. A root enters only through
// NewRootRegistry, from a genesis parameter, never at runtime.
type RootRegistry struct {
	roots []VendorRoot
}

// EmptyRoots is the default registry: it accepts no vendor.
func EmptyRoots() RootRegistry { return RootRegistry{} }

// NewRootRegistry validates roots and returns a registry holding them. Each root must be a
// known TEE kind and a self-signed CA certificate.
func NewRootRegistry(roots ...VendorRoot) (RootRegistry, error) {
	out := make([]VendorRoot, 0, len(roots))
	seen := map[[32]byte]bool{}
	for i, root := range roots {
		if root.Kind != KindSEVSNP && root.Kind != KindTDX {
			return RootRegistry{}, fmt.Errorf("vendor root %d has unknown TEE kind %q", i, root.Kind)
		}
		cert, err := x509.ParseCertificate(root.DER)
		if err != nil {
			return RootRegistry{}, fmt.Errorf("vendor root %d is not a certificate: %w", i, err)
		}
		if !cert.IsCA {
			return RootRegistry{}, fmt.Errorf("vendor root %d is not a CA certificate", i)
		}
		if err := cert.CheckSignatureFrom(cert); err != nil {
			return RootRegistry{}, fmt.Errorf("vendor root %d is not self-signed: %w", i, err)
		}
		fp := root.Fingerprint()
		if seen[fp] {
			return RootRegistry{}, fmt.Errorf("vendor root %d is listed twice", i)
		}
		seen[fp] = true
		out = append(out, VendorRoot{Kind: root.Kind, DER: append([]byte(nil), root.DER...)})
	}
	return RootRegistry{roots: out}, nil
}

// Len is how many roots the registry holds.
func (r RootRegistry) Len() int { return len(r.roots) }

// Has reports whether the registry holds a root for kind.
func (r RootRegistry) Has(kind TEEKind) bool {
	for _, root := range r.roots {
		if root.Kind == kind {
			return true
		}
	}
	return false
}
