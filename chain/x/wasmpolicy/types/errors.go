package types

import "cosmossdk.io/errors"

const (
	// NotLinkedCode is the coded error returned by every unwired wasm binding.
	NotLinkedCode = "NOT_LINKED"
)

var (
	// ErrUploadClosed is returned when MsgStoreCode runs before upload_sunset_height
	// for a code id that was not in the genesis code set.
	ErrUploadClosed = errors.Register(ModuleName, 1, "code upload is closed until upload_sunset_height")

	// ErrSunsetImmutable is returned when a message tries to change upload_sunset_height.
	// InitGenesis is the only writer, and it refuses a second write.
	ErrSunsetImmutable = errors.Register(ModuleName, 2, "upload_sunset_height cannot be changed by a message")

	// ErrIBCDisabled is returned when a contract message tries to open or use an IBC channel.
	ErrIBCDisabled = errors.Register(ModuleName, 4, "contract cannot open an IBC channel")

	// ErrNoramaWrapper is returned when a token wrapper creates or holds norama.
	ErrNoramaWrapper = errors.Register(ModuleName, 5, "token wrapper cannot create or hold norama")

	// ErrDepositPayer is returned when contract state grew and no account can be charged.
	ErrDepositPayer = errors.Register(ModuleName, 6, "no account can pay the state deposit for this contract call")

	// ErrDepositCap is returned when one transaction would lock more state deposit than max_deposit_per_tx.
	ErrDepositCap = errors.Register(ModuleName, 7, "state deposit for this transaction exceeds max_deposit_per_tx")

	// ErrDepositLedgerFull is returned when a contract already has max_deposit_chunks payers and a new one grows it.
	ErrDepositLedgerFull = errors.Register(ModuleName, 8, "contract has the maximum number of state-deposit payers")
)
