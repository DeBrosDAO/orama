use orama_shielded_wallet::keys::{
    account_key, master_key, shielded_seed, Account, KeyError, MASTER_PERSONALIZATION, ORAMA_COIN_TYPE,
};
use orama_shielded_wallet::vectors::{abandon_seed, bip39_seed, hex, key_vectors, KeyVectors, MNEMONICS};
use orchard::keys::{FullViewingKey, Scope, SpendingKey};
use zip32::AccountId;

const COMMITTED: &str = include_str!("../testdata/keys/keys.json");

/// The seed the RootWallet docs pin for the all-"abandon" phrase.
const ABANDON_SEED_HEX: &str = "5eb00bbddcf069084889a8ab9155568165f5c453ccb85e70811aaed6f6da5fc1\
9a5ac40b389cd370d086206dec8aa6c43daea6690f20ad3d8d48b2d2ce9e38e4";

#[test]
fn bip39_seed_matches_the_rootwallet_pin() {
    assert_eq!(hex(&abandon_seed()), ABANDON_SEED_HEX);
}

#[test]
fn committed_vectors_equal_a_fresh_derivation() {
    let committed: KeyVectors = serde_json::from_str(COMMITTED).expect("keys.json parses");
    assert_eq!(committed, key_vectors());
}

#[test]
fn vectors_cover_three_seeds_two_accounts_three_addresses() {
    let v = key_vectors();
    assert_eq!(v.seeds.len(), 3);
    for s in &v.seeds {
        assert_eq!(s.accounts.len(), 2);
        for a in &s.accounts {
            assert_eq!(a.addresses.len(), 3);
            assert_eq!(a.full_viewing_key.len(), 96 * 2);
            assert_eq!(a.addresses[0].len(), 43 * 2);
        }
    }
}

#[test]
fn hkdf_convention_reproduces_the_documented_rootwallet_branch() {
    // docs/CRYPTO_ARCHITECTURE.md pins orama-storage-v1 for this seed; the shielded branch uses
    // the same HKDF(salt = branch, info = "") construction.
    use hkdf::Hkdf;
    let mut out = [0u8; 32];
    Hkdf::<sha2::Sha256>::new(Some(b"orama-storage-v1"), &abandon_seed())
        .expand(&[], &mut out)
        .unwrap();
    assert_eq!(hex(&out), "9b164e82a47f07b828399215348b11067a247f0f996a8e8f1155b6b596122052");
}

#[test]
fn master_key_is_blake2b_512_with_the_orama_personalization() {
    let seed = abandon_seed();
    let s32 = shielded_seed(&seed).unwrap();
    let i = blake2b_simd::Params::new().hash_length(64).personal(&MASTER_PERSONALIZATION).hash(&s32[..]);
    let master = master_key(&seed).unwrap();
    assert_eq!(master.spending_key().to_bytes(), &i.as_bytes()[..32]);
    assert_eq!(master.chain_code()[..], i.as_bytes()[32..]);
}

#[test]
fn derivation_framework_equals_orchards_own_zip32_under_zcash_personalization() {
    // Swap only the personalization to Zcash's and orchard's public ZIP-32 entry point must agree,
    // which proves the child derivation here is ZIP-32's, not a lookalike.
    let s32 = shielded_seed(&abandon_seed()).unwrap();
    for account in 0..3u32 {
        let want = SpendingKey::from_zip32_seed(&s32[..], ORAMA_COIN_TYPE, AccountId::try_from(account).unwrap()).unwrap();
        let got = orama_shielded_wallet::keys::zcash_personalization_key_for_tests(&s32, ORAMA_COIN_TYPE, account);
        assert_eq!(got.to_bytes(), want.to_bytes(), "account {account}");
    }
}

#[test]
fn orama_keys_never_equal_zcash_keys_from_the_same_seed() {
    let seed = abandon_seed();
    let s32 = shielded_seed(&seed).unwrap();
    for coin in [133u32, 1, ORAMA_COIN_TYPE] {
        let zcash = SpendingKey::from_zip32_seed(&s32[..], coin, AccountId::ZERO).unwrap();
        let orama = account_key(&seed, 0).unwrap().spending_key();
        assert_ne!(zcash.to_bytes(), orama.to_bytes(), "coin {coin}");
        // Also against the raw BIP-39 seed as Zcash wallets would use it.
        let zcash_raw = SpendingKey::from_zip32_seed(&seed, 133, AccountId::ZERO).unwrap();
        assert_ne!(zcash_raw.to_bytes(), orama.to_bytes());
    }
}

#[test]
fn accounts_seeds_and_scopes_are_all_distinct() {
    let mut fvks = Vec::new();
    for m in MNEMONICS {
        let seed = bip39_seed(m);
        for account in 0..2 {
            fvks.push(Account::derive(&seed, account).unwrap().fvk.to_bytes());
        }
    }
    let n = fvks.len();
    fvks.sort();
    fvks.dedup();
    assert_eq!(fvks.len(), n);

    let a = Account::derive(&abandon_seed(), 0).unwrap();
    assert_ne!(a.fvk.to_ivk(Scope::External).to_bytes(), a.fvk.to_ivk(Scope::Internal).to_bytes());
    assert_ne!(a.address(0), a.address(1));
}

#[test]
fn chain_of_keys_is_consistent() {
    let a = Account::derive(&abandon_seed(), 1).unwrap();
    // FVK re-derives from the spending key and round-trips its encoding.
    assert_eq!(a.fvk.to_bytes(), FullViewingKey::from(&a.spending_key()).to_bytes());
    assert_eq!(FullViewingKey::from_bytes(&a.fvk.to_bytes()).unwrap().to_bytes(), a.fvk.to_bytes());
    // The address belongs to the FVK, external scope.
    let addr = orchard::Address::from_raw_address_bytes(&a.address(2)).unwrap();
    assert_eq!(a.fvk.scope_for_address(&addr), Some(Scope::External));
}

#[test]
fn derivation_is_deterministic() {
    let a = Account::derive(&abandon_seed(), 0).unwrap();
    let b = Account::derive(&abandon_seed(), 0).unwrap();
    assert_eq!(a.spending_key().to_bytes(), b.spending_key().to_bytes());
}

#[test]
fn seed_length_and_account_index_are_validated() {
    assert_eq!(shielded_seed(&[0u8; 32]).unwrap_err(), KeyError::SeedLength(32));
    assert_eq!(shielded_seed(&[]).unwrap_err(), KeyError::SeedLength(0));
    assert!(matches!(
        Account::derive(&abandon_seed(), 1 << 31),
        Err(KeyError::AccountIndex(_))
    ));
    assert!(Account::derive(&abandon_seed(), (1 << 31) - 1).is_ok());
}

// Secrets the crate hands out are wipe-on-drop types: this fails to compile if one becomes a plain
// array again.
#[test]
fn seeds_and_chain_codes_are_wiped_on_drop() {
    fn wiped<T: zeroize::ZeroizeOnDrop>(_: &T) {}
    let seed = abandon_seed();
    wiped(&shielded_seed(&seed).unwrap());
    wiped(&master_key(&seed).unwrap().chain_code());
    let a = Account::derive(&seed, 0).unwrap();
    wiped(&a.extended.chain_code());
    // The account keeps its spending key as bytes it wipes, and builds the key value on demand.
    assert_eq!(a.spending_key().to_bytes(), a.extended.spending_key().to_bytes());
}
