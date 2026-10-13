use orama_shielded_wallet::address::{decode, decode_orchard, encode, AddressError, DATA_LEN, ENCODED_LEN, HRP, RAW_LEN};
use orama_shielded_wallet::keys::Account;
use orama_shielded_wallet::vectors::abandon_seed;
use serde::Deserialize;

const COMMITTED: &str = include_str!("../testdata/addresses/addresses.json");

#[derive(Deserialize)]
struct Valid {
    raw: String,
    address: String,
}

#[derive(Deserialize)]
struct Invalid {
    why: String,
    address: String,
    error: String,
}

#[derive(Deserialize)]
struct Vectors {
    hrp: String,
    raw_length: usize,
    encoded_length: usize,
    valid: Vec<Valid>,
    accept: Vec<Valid>,
    invalid: Vec<Invalid>,
}

fn vectors() -> Vectors {
    serde_json::from_str(COMMITTED).expect("addresses.json parses")
}

fn raw(hex: &str) -> [u8; RAW_LEN] {
    hex::decode(hex).expect("hex").try_into().expect("43 bytes")
}

#[test]
fn the_constants_are_the_vector_files() {
    let v = vectors();
    assert_eq!(v.hrp, HRP);
    assert_eq!(v.raw_length, RAW_LEN);
    assert_eq!(v.encoded_length, ENCODED_LEN);
    assert_eq!(ENCODED_LEN, 6 + 1 + DATA_LEN);
}

#[test]
fn every_valid_vector_encodes_and_decodes_to_itself() {
    let v = vectors();
    assert!(v.valid.len() >= 8);
    for case in &v.valid {
        let bytes = raw(&case.raw);
        assert_eq!(encode(&bytes), case.address, "encode {}", case.raw);
        assert_eq!(case.address.len(), ENCODED_LEN);
        assert_eq!(decode(&case.address), Ok(bytes), "decode {}", case.address);
    }
}

#[test]
fn capitals_are_accepted() {
    for case in &vectors().accept {
        assert_eq!(decode(&case.address), Ok(raw(&case.raw)), "{}", case.address);
        assert_ne!(case.address, case.address.to_lowercase());
    }
}

#[test]
fn every_invalid_vector_is_refused_with_its_code() {
    let v = vectors();
    assert!(v.invalid.len() >= 15);
    for case in &v.invalid {
        let err = decode(&case.address).expect_err(&case.why);
        assert_eq!(err.code(), case.error, "{}: {}", case.why, case.address);
    }
}

/// The key tree's own addresses, derived here, are the vectors' first three: the committed file
/// is not just self-consistent but the wallet's output.
#[test]
fn the_wallet_addresses_encode_to_the_vectors() {
    let account = Account::derive(&abandon_seed(), 0).expect("account 0");
    let v = vectors();
    for j in 0..3u32 {
        assert_eq!(encode(&account.address(j)), v.valid[j as usize].address);
    }
}

#[test]
fn wallet_addresses_are_orchard_addresses_and_zero_is_not() {
    let account = Account::derive(&abandon_seed(), 0).expect("account 0");
    let text = encode(&account.address(1));
    let address = decode_orchard(&text).expect("a derived address pays");
    assert_eq!(address.to_raw_address_bytes(), account.address(1));
    // 43 zero bytes are a well-formed string and no address Orchard can pay.
    let zero = encode(&[0u8; RAW_LEN]);
    assert_eq!(decode(&zero), Ok([0u8; RAW_LEN]));
    assert_eq!(decode_orchard(&zero).err(), Some(AddressError::NotAnOrchardAddress));
}

#[test]
fn decode_never_panics_on_odd_input() {
    for s in ["", "1", "oramas1", "oramas1q", "ORAMAS1", "é1é", "oramas1\u{1F600}", &"1".repeat(200), &"q".repeat(500)] {
        assert!(decode(s).is_err(), "{s:?}");
    }
}
