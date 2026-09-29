//! Deterministic key-tree test vectors and the seeds they come from.

use hmac::Hmac;
use orchard::keys::Scope;
use serde::{Deserialize, Serialize};
use sha2::Sha512;

use crate::keys::{
    master_key, shielded_seed, Account, HKDF_BRANCH, MASTER_PERSONALIZATION, ORAMA_COIN_TYPE, ZIP32_PURPOSE,
};

/// Addresses listed per account in the vectors.
pub const ADDRESSES_PER_ACCOUNT: u32 = 3;
/// Accounts listed per seed.
pub const ACCOUNTS: u32 = 2;

/// BIP-39 test mnemonics: the RootWallet all-"abandon" phrase and two standard BIP-39 vectors.
pub const MNEMONICS: [&str; 3] = [
    "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
    "legal winner thank year wave sausage worth useful legal winner thank yellow",
    "zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo wrong",
];

pub fn hex(b: &[u8]) -> String {
    hex::encode(b)
}

/// BIP-39 seed: PBKDF2-HMAC-SHA512(mnemonic, "mnemonic", 2048 rounds), empty passphrase. The
/// mnemonic checksum is not validated here; the vectors use valid phrases.
pub fn bip39_seed(mnemonic: &str) -> [u8; 64] {
    let mut out = [0u8; 64];
    pbkdf2::pbkdf2::<Hmac<Sha512>>(mnemonic.as_bytes(), b"mnemonic", 2048, &mut out)
        .expect("HMAC accepts any key length");
    out
}

pub fn abandon_seed() -> [u8; 64] {
    bip39_seed(MNEMONICS[0])
}

#[derive(Serialize, Deserialize, PartialEq, Eq, Debug)]
pub struct AccountVector {
    pub account: u32,
    pub path: String,
    pub chain_code: String,
    pub spending_key: String,
    pub full_viewing_key: String,
    pub external_ivk: String,
    pub internal_ivk: String,
    pub external_ovk: String,
    pub internal_ovk: String,
    /// Raw 43-byte addresses at diversifier indices 0.., external scope.
    pub addresses: Vec<String>,
}

#[derive(Serialize, Deserialize, PartialEq, Eq, Debug)]
pub struct SeedVector {
    pub mnemonic: String,
    pub bip39_seed: String,
    pub shielded_seed: String,
    pub master_spending_key: String,
    pub master_chain_code: String,
    pub accounts: Vec<AccountVector>,
}

#[derive(Serialize, Deserialize, PartialEq, Eq, Debug)]
pub struct KeyVectors {
    pub hkdf_branch: String,
    pub master_personalization: String,
    pub coin_type: u32,
    pub purpose: u32,
    pub seeds: Vec<SeedVector>,
}

fn account_vector(seed: &[u8], account: u32) -> AccountVector {
    let a = Account::derive(seed, account).expect("valid account");
    let ovk = |s| hex(a.fvk.to_ovk(s).as_ref());
    AccountVector {
        account,
        path: format!("m/{ZIP32_PURPOSE}'/{ORAMA_COIN_TYPE}'/{account}'"),
        chain_code: hex(&a.extended.chain_code()),
        spending_key: hex(a.sk.to_bytes()),
        full_viewing_key: hex(&a.fvk.to_bytes()),
        external_ivk: hex(&a.fvk.to_ivk(Scope::External).to_bytes()),
        internal_ivk: hex(&a.fvk.to_ivk(Scope::Internal).to_bytes()),
        external_ovk: ovk(Scope::External),
        internal_ovk: ovk(Scope::Internal),
        addresses: (0..ADDRESSES_PER_ACCOUNT).map(|j| hex(&a.address(j))).collect(),
    }
}

pub fn key_vectors() -> KeyVectors {
    let seeds = MNEMONICS
        .iter()
        .map(|m| {
            let seed = bip39_seed(m);
            let master = master_key(&seed).expect("master");
            SeedVector {
                mnemonic: (*m).into(),
                bip39_seed: hex(&seed),
                shielded_seed: hex(&shielded_seed(&seed).expect("hkdf")),
                master_spending_key: hex(master.spending_key().to_bytes()),
                master_chain_code: hex(&master.chain_code()),
                accounts: (0..ACCOUNTS).map(|i| account_vector(&seed, i)).collect(),
            }
        })
        .collect();
    KeyVectors {
        hkdf_branch: String::from_utf8_lossy(HKDF_BRANCH).into(),
        master_personalization: String::from_utf8_lossy(&MASTER_PERSONALIZATION).into(),
        coin_type: ORAMA_COIN_TYPE,
        purpose: ZIP32_PURPOSE,
        seeds,
    }
}
