//go:build cgo && orchardffi

package orchard

/*
#cgo CFLAGS: -I${SRCDIR}/../../orchardffi/include
#cgo darwin,arm64 LDFLAGS: -L${SRCDIR}/lib/darwin_arm64
#cgo darwin,amd64 LDFLAGS: -L${SRCDIR}/lib/darwin_amd64
#cgo linux,amd64 LDFLAGS: -L${SRCDIR}/lib/linux_amd64
#cgo linux,arm64 LDFLAGS: -L${SRCDIR}/lib/linux_arm64
#cgo LDFLAGS: -lorama_orchard
#cgo linux LDFLAGS: -lm -ldl -lpthread
#cgo darwin LDFLAGS: -liconv
#include "orama_orchard.h"
*/
import "C"

import (
	"fmt"
	"unsafe"

	"github.com/DeBrosOfficial/network/chain/x/shielded/verify"
)

// Linked reports whether this build carries the Rust verifier.
const Linked = true

type verifier struct {
	chainID string
}

// New returns the Orchard/Ironwood verifier for one chain ID. It is safe for concurrent use. An
// empty chain ID is refused: the sighash would bind to no chain, and a bundle built for the empty
// chain would verify anywhere that forgot to set one.
func New(chainID string) (verify.Verifier, error) {
	if chainID == "" {
		return nil, verify.ErrEmptyChainID
	}
	return verifier{chainID: chainID}, nil
}

// Warm builds the Rust verifying key now. The key is otherwise built on the first bundle, which
// would put seconds of key generation inside a consensus handler. Call it once at start, before
// the node serves blocks.
func Warm() error {
	return codeToError(int32(C.orama_orchard_warm()))
}

func (verifier) ID() string { return VerifierID }

// Verify accepts a bundle only when its proof and every signature verify.
func (v verifier) Verify(bundle, binding []byte) error {
	if len(bundle) == 0 || len(bundle) > MaxBundleBytes {
		return fmt.Errorf("%w: %d bytes", verify.ErrMalformed, len(bundle))
	}
	sighash, err := Sighash(v.chainID, binding, bundle)
	if err != nil {
		return err
	}
	code := C.orama_orchard_verify(
		(*C.uint8_t)(unsafe.Pointer(&bundle[0])),
		C.size_t(len(bundle)),
		(*C.uint8_t)(unsafe.Pointer(&sighash[0])),
	)
	return codeToError(int32(code))
}

func codeToError(code int32) error {
	switch code {
	case C.ORAMA_ORCHARD_OK:
		return nil
	case C.ORAMA_ORCHARD_MALFORMED:
		return verify.ErrMalformed
	case C.ORAMA_ORCHARD_BAD_PROOF_LENGTH:
		return verify.ErrProofLength
	case C.ORAMA_ORCHARD_PROOF_REJECTED:
		return verify.ErrProofRejected
	case C.ORAMA_ORCHARD_SIGNATURE_REJECTED:
		return verify.ErrSignatureRejected
	case C.ORAMA_ORCHARD_PANIC:
		return fmt.Errorf("%w: the Rust verifier panicked", verify.ErrVerifierFault)
	default:
		return fmt.Errorf("%w: result code %d", verify.ErrVerifierFault, code)
	}
}
