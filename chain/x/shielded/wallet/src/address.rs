//! The text form of an Orama shielded address.
//!
//! A shielded address is the 43 raw bytes of an Orchard address (an 11-byte diversifier then the
//! 32-byte transmission key `pk_d`, [`crate::keys::Account::address`]) written in Bech32m
//! (BIP-350) with the human-readable part `oramas`. It is 82 characters: `oramas`, the separator
//! `1`, 69 data characters for the 344 bits plus one zero padding bit, and the 6-character
//! checksum. It is written in lowercase; a decoder also accepts it in all capitals (for a QR code)
//! and refuses a mix of cases.
//!
//! A decoder checks, in this order, and reports the first that fails ([`AddressError`]):
//! mixed case, no separator, a human-readable part other than `oramas`, a character outside the
//! Bech32 alphabet, a data part other than 75 characters, the Bech32m checksum (an address with the
//! original Bech32 checksum fails here), and a non-zero padding bit. The Go twin
//! (`core/pkg/shieldedaddr`) and the TypeScript one (`sdk/src/chain/shielded-address.ts`) follow
//! the same order and the same error codes, and all three read the one committed vector file,
//! `testdata/addresses/addresses.json`.
//!
//! The string codec is structural: it accepts any 43 bytes. [`decode_orchard`] also checks that
//! they are an address Orchard can pay (its diversified base is a curve point).

use bech32::primitives::decode::CheckedHrpstring;
use bech32::{Bech32m, Hrp};
use orchard::Address;

/// The human-readable part of a shielded address.
pub const HRP: &str = "oramas";
/// Bytes in a raw Orchard address.
pub const RAW_LEN: usize = 43;
/// Characters in the data part, checksum included: 69 for the address and 6 for the checksum.
pub const DATA_LEN: usize = 75;
/// Characters in an encoded address.
pub const ENCODED_LEN: usize = HRP.len() + 1 + DATA_LEN;

const CHARSET: &str = "qpzry9x8gf2tvdw0s3jn54khce6mua7l";
const CHECKSUM_LEN: usize = 6;

/// Why a string is not a shielded address. The `code` of each is the name the vector file uses.
#[derive(Debug, PartialEq, Eq, Clone, Copy)]
pub enum AddressError {
    MixedCase,
    NoSeparator,
    WrongPrefix,
    BadCharacter,
    WrongLength,
    BadChecksum,
    BadPadding,
    /// The bytes are not an address Orchard can pay (only [`decode_orchard`] reports this).
    NotAnOrchardAddress,
}

impl AddressError {
    pub fn code(&self) -> &'static str {
        match self {
            Self::MixedCase => "mixed_case",
            Self::NoSeparator => "no_separator",
            Self::WrongPrefix => "wrong_prefix",
            Self::BadCharacter => "bad_character",
            Self::WrongLength => "wrong_length",
            Self::BadChecksum => "bad_checksum",
            Self::BadPadding => "bad_padding",
            Self::NotAnOrchardAddress => "not_an_orchard_address",
        }
    }
}

impl std::fmt::Display for AddressError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        let what = match self {
            Self::MixedCase => "mixes upper and lower case",
            Self::NoSeparator => "has no '1' separator",
            Self::WrongPrefix => "does not start with oramas1",
            Self::BadCharacter => "has a character outside the Bech32 alphabet",
            Self::WrongLength => "is not 82 characters long",
            Self::BadChecksum => "has a wrong Bech32m checksum",
            Self::BadPadding => "has non-zero padding bits",
            Self::NotAnOrchardAddress => "does not hold a valid Orchard address",
        };
        write!(f, "not a shielded address: it {what}")
    }
}

impl std::error::Error for AddressError {}

/// The shielded address text of a raw Orchard address, in lowercase.
pub fn encode(raw: &[u8; RAW_LEN]) -> String {
    let hrp = Hrp::parse_unchecked(HRP);
    bech32::encode::<Bech32m>(hrp, raw).expect("43 bytes fit a Bech32m string")
}

/// The raw 43 bytes of a shielded address, or the first reason it is not one.
pub fn decode(s: &str) -> Result<[u8; RAW_LEN], AddressError> {
    let has_upper = s.chars().any(|c| c.is_ascii_uppercase());
    let has_lower = s.chars().any(|c| c.is_ascii_lowercase());
    if has_upper && has_lower {
        return Err(AddressError::MixedCase);
    }
    let lower = s.to_ascii_lowercase();
    let Some(sep) = lower.rfind('1') else {
        return Err(AddressError::NoSeparator);
    };
    if &lower[..sep] != HRP {
        return Err(AddressError::WrongPrefix);
    }
    let data = &lower[sep + 1..];
    if data.chars().any(|c| !CHARSET.contains(c)) {
        return Err(AddressError::BadCharacter);
    }
    if data.len() != DATA_LEN {
        return Err(AddressError::WrongLength);
    }
    let checked = CheckedHrpstring::new::<Bech32m>(&lower).map_err(|_| AddressError::BadChecksum)?;
    let words: Vec<u8> = checked
        .data_part_ascii_no_checksum()
        .iter()
        .map(|&c| CHARSET.find(char::from(c)).expect("the character set was checked") as u8)
        .collect();
    debug_assert_eq!(words.len(), DATA_LEN - CHECKSUM_LEN);
    five_to_eight(&words)
}

/// [`decode`], then a check that the bytes are an address Orchard can pay.
pub fn decode_orchard(s: &str) -> Result<Address, AddressError> {
    let raw = decode(s)?;
    Option::from(Address::from_raw_address_bytes(&raw)).ok_or(AddressError::NotAnOrchardAddress)
}

/// Regroups 5-bit values into bytes; the bits left over after the last byte must be zero.
fn five_to_eight(words: &[u8]) -> Result<[u8; RAW_LEN], AddressError> {
    let mut out = Vec::with_capacity(RAW_LEN);
    let (mut acc, mut bits) = (0u32, 0u32);
    for &w in words {
        acc = (acc << 5) | u32::from(w);
        bits += 5;
        while bits >= 8 {
            bits -= 8;
            out.push(((acc >> bits) & 0xff) as u8);
        }
    }
    if bits > 0 && (acc & ((1 << bits) - 1)) != 0 {
        return Err(AddressError::BadPadding);
    }
    out.try_into().map_err(|_| AddressError::WrongLength)
}
