package types

import "crypto/ed25519"

const (
	// RSAFingerprintLen is the SHA-1 digest length of a Tor RSA relay identity.
	RSAFingerprintLen = 20
	// Ed25519PubLen is the length of a relay ed25519 identity public key.
	Ed25519PubLen = ed25519.PublicKeySize
	// Ed25519SigLen is the length of an ed25519 signature.
	Ed25519SigLen = ed25519.SignatureSize
	// InputsRootLen is the SHA-256 length of a canonical inputs_root.
	InputsRootLen = 32

	// FlagExit is bit 0 of RelayObservation.Flags. The median of this bit is
	// the only flag that changes pay.
	FlagExit uint32 = 1

	// MaxChunkCount bounds how many chunks one reporter may use for one epoch.
	// 32 chunks of MaxEntriesPerChunk is 131072 relays, above 10x a public-Tor
	// sized set.
	MaxChunkCount uint32 = 32
	// MaxEntriesPerChunk bounds one MsgReportEpoch.
	MaxEntriesPerChunk = 4096
	// MaxReporters bounds the genesis and governance reporter set.
	MaxReporters = 128

	// ReportWindowEpochs is how many emission epochs a closed epoch stays open
	// to reports. Epoch e closes when the chain enters e+1 and is reported on
	// during e+1 (ReportWindowEpochs = 1); it settles in the first block of e+2.
	// It is a constant, not a parameter: it only has to stay well inside the
	// trailing window in which x/emission keeps a closed epoch's ceiling.
	ReportWindowEpochs uint64 = 1
	// MaxReportEpoch is the highest epoch a stored report may name, so that
	// epoch + ReportWindowEpochs cannot wrap.
	MaxReportEpoch = ^uint64(0) - ReportWindowEpochs
)

// MaxEntriesPerReport is the most observations one reassembled report may carry.
func MaxEntriesPerReport() int {
	return int(MaxChunkCount) * MaxEntriesPerChunk
}
