package keeper

import "fmt"

// VerifierHalt is the panic value that stops a node whose shielded verifier faulted while it
// executed a block (checkInBlock). It is a panic, not an error, because an error would become the
// transaction's result and this node alone would record it. The app lets it through the
// transaction runner's recovery (chain/app shielded.go), which logs it and ends the process with
// a distinct exit code (chain/app ExitVerifierHalt): CometBFT's consensus routine would otherwise
// recover the panic and leave a live node that no longer commits. The block is not committed, and
// systemd restarts the node, which executes the same block again.
type VerifierHalt struct {
	Height int64
	// BundleHash is the hex SHA-256 of the bundle being checked: the bundle that was in flight when
	// the verifier failed. If every node halts on the same hash, the bundle is what kills the
	// verifier (see the poison bundle gap in the shielded chapter).
	BundleHash string
	Err        error
}

func (h VerifierHalt) Error() string {
	return fmt.Sprintf("the shielded verifier failed while this node executed block %d (bundle sha256 %s), so it cannot reach the same verdict as its peers "+
		"and stops instead of recording a result of its own: %v. "+
		"Fix the verifier and restart the node, which executes the block again: check that orama-orchard-verifier "+
		"(--shielded-verifier) exists, matches the pin linked into oramad and can run, that the machine has memory "+
		"(an OOM kill of the child is the usual cause), and read the lines above in the log. If every node stops on this same bundle hash, the bundle kills the verifier on every machine and the chain needs a fixed oramad", h.Height, h.BundleHash, h.Err)
}

func (h VerifierHalt) Unwrap() error { return h.Err }
