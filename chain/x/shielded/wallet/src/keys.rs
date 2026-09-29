//! Orama shielded key tree: ZIP-32 hardened-only Orchard derivation with an Orama personalization.
//!
//! ```text
//! BIP-39 seed (64 bytes)
//!   -> HKDF-SHA256(salt = "orama-shielded-v1", info = "", L = 32)      RootWallet branch convention
//!   -> master   = BLAKE2b-512("OramaIP32Orchard", seed32)              (sk, chain code)
//!   -> m/32'/ORAMA_COIN_TYPE'/account'                                 ZIP-32 child derivation
//!   -> SpendingKey -> FullViewingKey -> IncomingViewingKey (external/internal) -> Address
//! ```
//!
//! Child derivation is exactly ZIP-32's Orchard one (`PRF^expand` domain 0x81). Only the master
//! key personalization differs from Zcash, so no Orama key can equal a Zcash Orchard key derived
//! from the same seed, and the HKDF step keeps the tree disjoint from every other RootWallet
//! branch of the same seed.

use hkdf::Hkdf;
use orchard::keys::{FullViewingKey, Scope, SpendingKey};
use sha2::Sha256;
use zcash_spec::{PrfExpand, VariableLengthSlice};
use zip32::hardened_only::{Context, HardenedOnlyKey};
use zip32::ChildIndex;

/// RootWallet HKDF branch for the shielded tree (`docs/CRYPTO_ARCHITECTURE.md` convention:
/// salt = branch, info = empty).
pub const HKDF_BRANCH: &[u8] = b"orama-shielded-v1";
/// Length of a BIP-39 seed.
pub const BIP39_SEED_LEN: usize = 64;
/// 16-byte BLAKE2b personalization of the master key. Zcash uses `ZcashIP32Orchard`.
pub const MASTER_PERSONALIZATION: [u8; 16] = *b"OramaIP32Orchard";
/// ZIP-32 purpose, hardened.
pub const ZIP32_PURPOSE: u32 = 32;
/// Orama shielded coin type, hardened: ASCII "ORAM" = 0x4F52414D. Not a SLIP-44 registration and
/// unrelated to the Cosmos account coin type (118). Below 2^31 as ZIP-32 requires.
pub const ORAMA_COIN_TYPE: u32 = 0x4F52_414D;

#[derive(Debug, PartialEq, Eq)]
pub enum KeyError {
    /// The BIP-39 seed is not 64 bytes.
    SeedLength(usize),
    /// The derived bytes are not a valid Orchard spending key (probability about 2^-254).
    InvalidSpendingKey,
    /// The account index does not fit a hardened child index.
    AccountIndex(u32),
    /// The HKDF expand step failed (cannot happen for L = 32).
    Hkdf,
}

impl std::fmt::Display for KeyError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            KeyError::SeedLength(n) => write!(f, "seed is {n} bytes, a BIP-39 seed is {BIP39_SEED_LEN}"),
            KeyError::InvalidSpendingKey => write!(f, "derived bytes are not a valid Orchard spending key"),
            KeyError::AccountIndex(a) => write!(f, "account index {a} is not below 2^31"),
            KeyError::Hkdf => write!(f, "HKDF expand failed"),
        }
    }
}

impl std::error::Error for KeyError {}

/// ZIP-32 hardened-only context with Orama's master personalization and Orchard's child domain.
struct OramaOrchard;

impl Context for OramaOrchard {
    const MKG_DOMAIN: [u8; 16] = MASTER_PERSONALIZATION;
    const CKD_DOMAIN: PrfExpand<([u8; 32], [u8; 4], [u8; 1], VariableLengthSlice)> =
        PrfExpand::ORCHARD_ZIP32_CHILD;
}

/// The 32-byte ZIP-32 seed derived from the RootWallet BIP-39 seed.
pub fn shielded_seed(bip39_seed: &[u8]) -> Result<[u8; 32], KeyError> {
    if bip39_seed.len() != BIP39_SEED_LEN {
        return Err(KeyError::SeedLength(bip39_seed.len()));
    }
    let mut out = [0u8; 32];
    Hkdf::<Sha256>::new(Some(HKDF_BRANCH), bip39_seed)
        .expand(&[], &mut out)
        .map_err(|_| KeyError::Hkdf)?;
    Ok(out)
}

/// An extended key: a spending key and its chain code, at some depth of the tree.
pub struct ExtendedKey {
    key: HardenedOnlyKey<OramaOrchard>,
}

impl ExtendedKey {
    /// Master key from the 32-byte shielded seed.
    pub fn master(shielded_seed: &[u8; 32]) -> Result<Self, KeyError> {
        Self::checked(HardenedOnlyKey::master(&[shielded_seed]))
    }

    /// Hardened child. Every level must be a valid spending key, as in orchard's own ZIP-32.
    pub fn derive_hardened(&self, index: u32) -> Result<Self, KeyError> {
        if index >= 1 << 31 {
            return Err(KeyError::AccountIndex(index));
        }
        Self::checked(self.key.derive_child(ChildIndex::hardened(index)))
    }

    fn checked(key: HardenedOnlyKey<OramaOrchard>) -> Result<Self, KeyError> {
        if Option::<SpendingKey>::from(SpendingKey::from_bytes(*key.parts().0)).is_none() {
            return Err(KeyError::InvalidSpendingKey);
        }
        Ok(Self { key })
    }

    pub fn spending_key(&self) -> SpendingKey {
        Option::from(SpendingKey::from_bytes(*self.key.parts().0)).expect("checked at construction")
    }

    pub fn chain_code(&self) -> [u8; 32] {
        *self.key.parts().1.as_bytes()
    }
}

/// The master key of a wallet: HKDF branch, then Orama-personalized ZIP-32 master.
pub fn master_key(bip39_seed: &[u8]) -> Result<ExtendedKey, KeyError> {
    ExtendedKey::master(&shielded_seed(bip39_seed)?)
}

/// The account key at `m/32'/ORAMA_COIN_TYPE'/account'`.
pub fn account_key(bip39_seed: &[u8], account: u32) -> Result<ExtendedKey, KeyError> {
    master_key(bip39_seed)?
        .derive_hardened(ZIP32_PURPOSE)?
        .derive_hardened(ORAMA_COIN_TYPE)?
        .derive_hardened(account)
}

/// Everything a wallet holds for one account.
pub struct Account {
    pub extended: ExtendedKey,
    pub sk: SpendingKey,
    pub fvk: FullViewingKey,
}

impl Account {
    pub fn derive(bip39_seed: &[u8], account: u32) -> Result<Self, KeyError> {
        let extended = account_key(bip39_seed, account)?;
        let sk = extended.spending_key();
        let fvk = FullViewingKey::from(&sk);
        Ok(Self { extended, sk, fvk })
    }

    /// The raw 43-byte Orchard address (11-byte diversifier, 32-byte pk_d) at index `j`, external
    /// scope. No text encoding is defined for Orama addresses yet.
    pub fn address(&self, j: u32) -> [u8; 43] {
        self.fvk.address_at(j, Scope::External).to_raw_address_bytes()
    }
}

/// Derives `m/32'/coin'/account'` under Zcash's own `ZcashIP32Orchard` personalization. Exists only
/// so tests can show this framework equals orchard's ZIP-32 before the personalization is swapped.
#[doc(hidden)]
pub fn zcash_personalization_key_for_tests(seed: &[u8; 32], coin_type: u32, account: u32) -> SpendingKey {
    struct Zcash;
    impl Context for Zcash {
        const MKG_DOMAIN: [u8; 16] = *b"ZcashIP32Orchard";
        const CKD_DOMAIN: PrfExpand<([u8; 32], [u8; 4], [u8; 1], VariableLengthSlice)> =
            PrfExpand::ORCHARD_ZIP32_CHILD;
    }
    let mut k = HardenedOnlyKey::<Zcash>::master(&[seed]);
    for i in [ZIP32_PURPOSE, coin_type, account] {
        k = k.derive_child(ChildIndex::hardened(i));
    }
    Option::from(SpendingKey::from_bytes(*k.parts().0)).unwrap()
}
