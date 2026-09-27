// Package params holds the chain-wide constants that both the app wiring and individual
// modules (notably x/emission) need to agree on: the denom, its decimals, the bech32 prefix and
// the BIP-44 coin type. Keeping them in one leaf package (with no dependency on the app or any
// module) avoids scattering magic strings and numbers across the codebase.
package params

const (
	// BaseDenom is the chain's base (smallest-unit) denom, as decided in
	// plans/open-network.md ("Supply and emission") and
	// plans/open-network/track-c-chain.md (C1 "Denom and accounts").
	BaseDenom = "norama"

	// DisplayDenom is the human-facing unit. 1 ORAMA == NoramaPerOrama norama.
	DisplayDenom = "ORAMA"

	// DenomDecimals is the number of decimal places between BaseDenom and DisplayDenom.
	DenomDecimals = 9

	// NoramaPerOrama is 10^DenomDecimals: the number of norama in one ORAMA.
	NoramaPerOrama = 1_000_000_000

	// Bech32Prefix is the account address bech32 human-readable prefix. Validator operator and
	// consensus addresses use the conventional "valoper"/"valcons" suffixes derived from it.
	Bech32Prefix = "orama"

	// Bech32PrefixValAddr is the bech32 prefix for validator operator addresses.
	Bech32PrefixValAddr = Bech32Prefix + "valoper"
	// Bech32PrefixValPub is the bech32 prefix for validator operator public keys.
	Bech32PrefixValPub = Bech32Prefix + "valoperpub"
	// Bech32PrefixConsAddr is the bech32 prefix for validator consensus addresses.
	Bech32PrefixConsAddr = Bech32Prefix + "valcons"
	// Bech32PrefixConsPub is the bech32 prefix for validator consensus public keys.
	Bech32PrefixConsPub = Bech32Prefix + "valconspub"
	// Bech32PrefixAccPub is the bech32 prefix for account public keys.
	Bech32PrefixAccPub = Bech32Prefix + "pub"

	// CoinType is the BIP-44 coin type used to derive Orama accounts from a RootWallet seed.
	// plans/open-network.md D8/D1 marks the final derivation as a still-pending decision record;
	// until that record lands, 118 (the Cosmos SDK's own registered coin type) is used so
	// standard tooling (RootWallet, cosmos key derivation libraries) works unmodified. Kept as a
	// single named constant so the eventual final value only needs to change here.
	CoinType = 118
)
