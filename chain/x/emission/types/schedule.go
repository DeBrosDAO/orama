package types

import (
	"cosmossdk.io/math"

	"github.com/DeBrosOfficial/network/chain/app/params"
)

// EpochsPerHalvingBracket is how many completed epochs each halving bracket covers before the
// per-epoch maximum halves again (plans/open-network/track-c-chain.md C3).
const EpochsPerHalvingBracket = 730

// halvingBracketsOrama holds the maximum ORAMA (whole units, not norama) mintable per epoch for
// each of the five 730-epoch halving brackets, in order:
//
//	epochs    1-730:  14,848 ORAMA/epoch
//	epochs  731-1460:  7,424 ORAMA/epoch
//	epochs 1461-2190:  3,712 ORAMA/epoch
//	epochs 2191-2920:  1,856 ORAMA/epoch
//	epochs 2921-3650:    928 ORAMA/epoch
//
// This table is ossified (plans/open-network.md D18 and D12): only a hard fork can change it.
var halvingBracketsOrama = [5]int64{14848, 7424, 3712, 1856, 928}

// TailPerEpochOrama is the permanent per-epoch maximum, in whole ORAMA, from epoch
// FinalHalvingEpoch+1 onward, forever (plans/open-network/track-c-chain.md C3).
const TailPerEpochOrama = 274

// FinalHalvingEpoch is the last epoch covered by the halving table (epoch 3650); the tail applies
// from FinalHalvingEpoch+1 onward.
const FinalHalvingEpoch = uint64(len(halvingBracketsOrama)) * EpochsPerHalvingBracket

// MaxSafeEpoch bounds every epoch number this package's functions and x/emission's queries will
// accept. At one epoch a day this is well over 2.7 million years, so it is not a realistic limit
// in practice; it exists only to reject a client-supplied epoch big enough to make a query
// pointlessly expensive, and to document the bound genesis validation enforces on
// EpochState.CurrentEpoch.
const MaxSafeEpoch = 1_000_000_000

// oramaToNorama converts a whole-ORAMA amount into norama (params.NoramaPerOrama norama per
// ORAMA). It is the one place this package touches the app-wide denom constant.
func oramaToNorama(wholeOrama int64) math.Int {
	return math.NewInt(wholeOrama).MulRaw(params.NoramaPerOrama)
}

// MaxMintableForEpoch returns the schedule's maximum mintable amount, in norama, for the given
// completed epoch number (1-based; epoch 0 mints nothing). Halving boundaries count completed
// epochs, never calendar time (plans/open-network/track-c-chain.md C3).
func MaxMintableForEpoch(epoch uint64) math.Int {
	if epoch == 0 {
		return math.ZeroInt()
	}
	bracket := (epoch - 1) / EpochsPerHalvingBracket
	if bracket >= uint64(len(halvingBracketsOrama)) {
		return oramaToNorama(TailPerEpochOrama)
	}
	return oramaToNorama(halvingBracketsOrama[bracket])
}

// CumulativeScheduleMax returns the schedule's maximum possible cumulative mint, in norama,
// summed over epochs 1..epoch inclusive (epoch 0 returns zero). It is a maximum: the real
// cumulative mint is lower whenever a validator-share mint is skipped (which never happens once
// an epoch closes) or, for the non-validator shares, whenever a ceiling goes unclaimed - but this
// module only ever mints the validator share, so CumulativeScheduleMax as used by this module's
// invariant is the schedule's 100% total, comfortably above the ~60% actually minted.
//
// Every multiplication here uses math.Int (arbitrary precision) rather than converting a block or
// epoch count to int64, so this never overflows regardless of how large epoch is; callers that
// want to reject an unreasonably large epoch instead of computing its (enormous but correct)
// result should check epoch against MaxSafeEpoch first.
func CumulativeScheduleMax(epoch uint64) math.Int {
	if epoch == 0 {
		return math.ZeroInt()
	}

	total := math.ZeroInt()
	fullBrackets := epoch / EpochsPerHalvingBracket
	if fullBrackets > uint64(len(halvingBracketsOrama)) {
		fullBrackets = uint64(len(halvingBracketsOrama))
	}

	var i uint64
	for ; i < fullBrackets; i++ {
		bracketTotal := oramaToNorama(halvingBracketsOrama[i]).MulRaw(EpochsPerHalvingBracket)
		total = total.Add(bracketTotal)
	}

	remaining := epoch - fullBrackets*EpochsPerHalvingBracket
	if remaining == 0 {
		return total
	}
	remainingInt := math.NewIntFromUint64(remaining)
	if fullBrackets < uint64(len(halvingBracketsOrama)) {
		return total.Add(oramaToNorama(halvingBracketsOrama[fullBrackets]).Mul(remainingInt))
	}
	return total.Add(oramaToNorama(TailPerEpochOrama).Mul(remainingInt))
}

// CumulativeValidatorMinted returns the exact cumulative validator/delegator share minted over
// epochs 1..epoch inclusive, i.e. exactly what x/emission's CloseEpoch mints in total over that
// many closed epochs (epoch 0 returns zero). It is the closed-form equivalent of summing
// SplitEpochMint(MaxMintableForEpoch(e)).Validator for e in 1..epoch, and is exact - not an
// approximation - because every schedule amount is a whole number of ORAMA times
// params.NoramaPerOrama (10^9), and 10^9 is evenly divisible by 100: every share of every epoch's
// maximum divides with zero remainder, so there is never a remainder for the validator share to
// absorb (see TestSplitEpochMint_exactDivision) and per-epoch validator shares within a bracket
// are identical, letting them be summed by multiplication instead of a loop.
func CumulativeValidatorMinted(epoch uint64) math.Int {
	if epoch == 0 {
		return math.ZeroInt()
	}

	total := math.ZeroInt()
	fullBrackets := epoch / EpochsPerHalvingBracket
	if fullBrackets > uint64(len(halvingBracketsOrama)) {
		fullBrackets = uint64(len(halvingBracketsOrama))
	}

	var i uint64
	for ; i < fullBrackets; i++ {
		perEpoch := SplitEpochMint(oramaToNorama(halvingBracketsOrama[i])).Validator
		total = total.Add(perEpoch.MulRaw(EpochsPerHalvingBracket))
	}

	remaining := epoch - fullBrackets*EpochsPerHalvingBracket
	if remaining == 0 {
		return total
	}
	remainingInt := math.NewIntFromUint64(remaining)
	if fullBrackets < uint64(len(halvingBracketsOrama)) {
		perEpoch := SplitEpochMint(oramaToNorama(halvingBracketsOrama[fullBrackets])).Validator
		return total.Add(perEpoch.Mul(remainingInt))
	}
	perEpoch := SplitEpochMint(oramaToNorama(TailPerEpochOrama)).Validator
	return total.Add(perEpoch.Mul(remainingInt))
}
