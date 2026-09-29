//go:build cgo && orchardffi

package orchard

/*
#include "orama_orchard.h"
*/
import "C"

import (
	"fmt"
	"unsafe"

	"github.com/DeBrosOfficial/network/chain/x/shielded/verify"
)

// Append adds the commitments, in order, to the tree whose frontier is given and returns the
// new frontier and root.
func (Tree) Append(frontier []byte, commitments [][NodeLen]byte) ([]byte, [NodeLen]byte, error) {
	var root [NodeLen]byte
	flat := make([]byte, 0, len(commitments)*NodeLen)
	for i := range commitments {
		flat = append(flat, commitments[i][:]...)
	}
	out := make([]byte, MaxFrontierLen)
	var outLen C.size_t
	code := C.orama_orchard_tree_append(
		bytePtr(frontier), C.size_t(len(frontier)),
		bytePtr(flat), C.size_t(len(commitments)),
		(*C.uint8_t)(unsafe.Pointer(&out[0])), C.size_t(len(out)), &outLen,
		(*C.uint8_t)(unsafe.Pointer(&root[0])),
	)
	switch int32(code) {
	case C.ORAMA_ORCHARD_OK:
		return out[:outLen], root, nil
	case C.ORAMA_ORCHARD_MALFORMED:
		return nil, root, ErrTreeRejected
	default:
		return nil, root, fmt.Errorf("%w: tree append result code %d", verify.ErrVerifierFault, int32(code))
	}
}

// EmptyRoot is the root of a tree with no commitments, the anchor of a bundle that spends
// nothing real.
func (t Tree) EmptyRoot() ([NodeLen]byte, error) {
	_, root, err := t.Append(nil, nil)
	return root, err
}

func bytePtr(b []byte) *C.uint8_t {
	if len(b) == 0 {
		return nil
	}
	return (*C.uint8_t)(unsafe.Pointer(&b[0]))
}
