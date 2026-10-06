package types

import "errors"

var (
	// ErrNotReporter is returned when MsgReportEpoch is signed by an address
	// that is not in the reporter set.
	ErrNotReporter = errors.New("not a reporter")
	// ErrReporterChangeForbidden is returned when MsgUpdateReporters runs
	// without AllowReporterChange on the context. x/houses sets that flag
	// only for a passed structural proposal. This module never sets it.
	ErrReporterChangeForbidden = errors.New("reporter set changes only when the caller sets AllowReporterChange for a passed structural proposal")
	// ErrCrossCertMismatch is returned when the ed25519 identity signature
	// does not cover the RSA fingerprint.
	ErrCrossCertMismatch = errors.New("rsa fingerprint does not match the ed25519 cross-signature")
	// ErrEd25519Mismatch is returned when a report's ed25519 id is not the
	// key that cross-signed the registered RSA fingerprint.
	ErrEd25519Mismatch = errors.New("ed25519 identity does not match the registered relay")
	// ErrInputsRootMismatch is returned when inputs_root is not the SHA-256
	// of the canonical entries.
	ErrInputsRootMismatch = errors.New("inputs_root does not match the canonical entries")
)
