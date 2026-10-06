//! Writes the committed test vectors: one 1-action and one 2-action Ironwood bundle in the
//! canonical v6 encoding, each with its Orama sighash. Test tooling only: this proves, and the
//! node never proves.
//!
//!   cargo run --release --example gen_vectors -- testdata
//!
//! The sighash definition below must equal the Go one in x/shielded/verify/orchard/sighash.go;
//! the Go tests recompute it from the bundle bytes and compare.

use std::fs;
use std::path::PathBuf;

use orchard::builder::{Builder, BundleType};
use orchard::bundle::{Flags, BundleVersion};
use orchard::circuit::{OrchardCircuitVersion, ProvingKey};
use orchard::keys::{FullViewingKey, Scope, SpendingKey};
use orchard::tree::MerkleHashOrchard;
use orchard::value::NoteValue;
use incrementalmerkletree::Hashable;
use rand::rngs::OsRng;
use sha2::{Digest, Sha256};
use zcash_primitives::transaction::components::orchard::write_v6_bundle;

const CHAIN_ID: &str = "orama-localnet-orchard-vector-1";
const SIGHASH_DOMAIN: &[u8] = b"orama-shielded-ironwood-sighash-v1";
const ACTION_LEN: usize = 820;
const HEADER_LEN: usize = 41;

fn sighash(chain_id: &str, bundle: &[u8]) -> [u8; 32] {
    let n = bundle[0] as usize; // one byte is enough for the vectors (n < 253)
    let prefix = &bundle[..1 + n * ACTION_LEN + HEADER_LEN];
    let mut h = Sha256::new();
    h.update(SIGHASH_DOMAIN);
    h.update((chain_id.len() as u16).to_be_bytes());
    h.update(chain_id.as_bytes());
    h.update(prefix);
    h.finalize().into()
}

fn build(pk: &ProvingKey, bundle_type: BundleType, name: &str, out: &PathBuf) {
    let mut rng = OsRng;
    let sk = SpendingKey::from_bytes([7; 32]).unwrap();
    let fvk = FullViewingKey::from(&sk);
    let recipient = fvk.address_at(0u32, Scope::External);
    let anchor = MerkleHashOrchard::empty_root(32.into()).into();

    let mut builder = Builder::new(
        bundle_type,
        BundleVersion::ironwood_v3(),
        Flags::SPENDS_DISABLED,
        anchor,
    )
    .expect("shielding flags are valid for Ironwood");
    builder
        .add_output(None, recipient, NoteValue::from_raw(5000), [0u8; 512])
        .expect("output");
    let (unauthorized, _) = builder.build::<i64>(&mut rng).unwrap().unwrap();
    let proven = unauthorized.create_proof(pk, &mut rng).unwrap();

    // The bytes before the proof do not depend on signatures, so a throwaway signing pass
    // yields the prefix the real sighash covers.
    let probe = proven
        .clone()
        .apply_signatures(&mut rng, [0u8; 32], &[])
        .unwrap();
    let mut probe_bytes = Vec::new();
    let probe = probe
        .try_map_value_balance::<_, (), _>(|v| {
            zcash_protocol::value::ZatBalance::from_i64(v).map_err(|_| ())
        })
        .unwrap();
    write_v6_bundle(Some(&probe), &mut probe_bytes).unwrap();
    let hash = sighash(CHAIN_ID, &probe_bytes);

    let bundle = proven
        .apply_signatures(&mut rng, hash, &[])
        .unwrap()
        .try_map_value_balance::<_, (), _>(|v| {
            zcash_protocol::value::ZatBalance::from_i64(v).map_err(|_| ())
        })
        .unwrap();
    let mut bytes = Vec::new();
    write_v6_bundle(Some(&bundle), &mut bytes).unwrap();
    assert_eq!(sighash(CHAIN_ID, &bytes), hash, "prefix must not depend on signatures");
    assert_eq!(orama_orchard::verify(&bytes, &hash), orama_orchard::OK);

    fs::write(out.join(format!("{name}.bundle")), &bytes).unwrap();
    fs::write(out.join(format!("{name}.sighash")), hash).unwrap();
    println!("{name}: {} bytes", bytes.len());
}

fn main() {
    let out = PathBuf::from(std::env::args().nth(1).expect("output directory"));
    fs::create_dir_all(&out).unwrap();
    let pk = ProvingKey::build(OrchardCircuitVersion::PostNu6_3);
    build(&pk, BundleType::UNPADDED, "ironwood-1-action", &out);
    build(&pk, BundleType::DEFAULT, "ironwood-2-action", &out);
    fs::write(out.join("chain-id"), CHAIN_ID).unwrap();
}
