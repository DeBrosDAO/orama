//! Writes the committed flow vectors, which continue the shielding bundle `ironwood-1-action`:
//!
//!   * `ironwood-transfer`: spends that bundle's 5000-norama note and pays 100 as the fee
//!     (value balance +100), so it is a signer-less shielded transfer.
//!   * `ironwood-unshield` and `ironwood-unshield-bond`: each spends the transfer's 4900-norama
//!     change note and takes all of it out (value balance +4900), so they are unshields, and they
//!     are alternatives: they spend the same note. Each is signed for one signer and one target,
//!     the first a fee top-up to that signer's own earnings, the second a delegation to one
//!     validator, so its sighash is the bound one: the same bundle under any other signer or
//!     target fails its signatures.
//!
//! Each is proven against the note-commitment tree the chain builds when it appends the earlier
//! bundles' commitments in order, so a chain test that replays shield, transfer, unshield checks
//! the FFI tree and the anchor window against real proofs. Test tooling only: this proves, and the
//! node never proves.
//!
//!   cargo run --release --example gen_flow_vectors -- testdata \
//!     5f64e1128afde934c792939c8e64fca46b7f71f6 01cf624b3ec99d3f174f9e431733f31659a3eee7
//!
//! The second argument is the hex address of the test signer the unshields are bound to, the third
//! that of the validator the bond one delegates to. chain/app/shielded_helpers_test.go derives
//! them with GenPrivKeyFromSecret("orama-shielded-test-alice") and
//! GenPrivKeyFromSecret("orama-shielded-test-committee-0").

use std::fs;
use std::io::Cursor;
use std::path::PathBuf;

use incrementalmerkletree::{Hashable, Level};
use orchard::builder::{Builder, BundleType};
use orchard::bundle::{Authorized, BundleVersion, Flags};
use orchard::circuit::{OrchardCircuitVersion, ProvingKey};
use orchard::keys::{FullViewingKey, Scope, SpendAuthorizingKey, SpendingKey};
use orchard::tree::{MerkleHashOrchard, MerklePath};
use orchard::value::NoteValue;
use orchard::{Anchor, Bundle, Note, ValuePool};
use rand::rngs::OsRng;
use sha2::{Digest, Sha256};
use zcash_primitives::transaction::components::orchard::{read_v6_bundle, write_v6_bundle};
use zcash_protocol::consensus::BranchId;
use zcash_protocol::value::ZatBalance;

const CHAIN_ID: &str = "orama-orchard-vector-1";
const SIGHASH_DOMAIN: &[u8] = b"orama-shielded-ironwood-sighash-v1";
const SIGHASH_BOUND_DOMAIN: &[u8] = b"orama-shielded-ironwood-sighash-bound-v1";
/// UNSHIELD_TARGET_BOND and UNSHIELD_TARGET_FEE_TOPUP in proto/orama/shielded/v1/shielded.proto.
const TARGET_BOND: u8 = 1;
const TARGET_FEE_TOPUP: u8 = 3;
const ACTION_LEN: usize = 820;
const HEADER_LEN: usize = 41;
const DEPTH: u8 = 32;

/// The binding of an unshield: u8(len(signer)) || signer || u8(target) || u8(len(validator)) ||
/// validator || u16be(len(node_id)) || node_id || u8(role), here with no node and no role. See
/// MsgUnshield.Binding in chain/x/shielded/types/msgs.go.
fn binding(signer: &[u8], target: u8, validator: &[u8]) -> Vec<u8> {
    let mut b = vec![signer.len() as u8];
    b.extend_from_slice(signer);
    b.extend_from_slice(&[target, validator.len() as u8]);
    b.extend_from_slice(validator);
    b.extend_from_slice(&[0, 0, 0]);
    b
}

fn hex(arg: usize, what: &str) -> Vec<u8> {
    let text = std::env::args().nth(arg).unwrap_or_else(|| panic!("hex address of {what}"));
    (0..text.len() / 2)
        .map(|i| u8::from_str_radix(&text[2 * i..2 * i + 2], 16).expect("hex"))
        .collect()
}

fn sighash(bundle: &[u8], binding: Option<&[u8]>) -> [u8; 32] {
    let n = bundle[0] as usize;
    let prefix = &bundle[..1 + n * ACTION_LEN + HEADER_LEN];
    let mut h = Sha256::new();
    h.update(if binding.is_some() { SIGHASH_BOUND_DOMAIN } else { SIGHASH_DOMAIN });
    h.update((CHAIN_ID.len() as u16).to_be_bytes());
    h.update(CHAIN_ID.as_bytes());
    if let Some(binding) = binding {
        h.update((binding.len() as u32).to_be_bytes());
        h.update(binding);
    }
    h.update(prefix);
    h.finalize().into()
}

/// The hash of the subtree of `height` whose first leaf index is `index << height`.
fn node(leaves: &[MerkleHashOrchard], height: u8, index: u64) -> MerkleHashOrchard {
    if (index << height) >= leaves.len() as u64 {
        return MerkleHashOrchard::empty_root(Level::from(height));
    }
    if height == 0 {
        return leaves[index as usize];
    }
    let left = node(leaves, height - 1, index * 2);
    let right = node(leaves, height - 1, index * 2 + 1);
    MerkleHashOrchard::combine(Level::from(height - 1), &left, &right)
}

fn path(leaves: &[MerkleHashOrchard], position: u64) -> (MerklePath, Anchor) {
    let auth: Vec<MerkleHashOrchard> = (0..DEPTH)
        .map(|level| node(leaves, level, (position >> level) ^ 1))
        .collect();
    let auth: [MerkleHashOrchard; 32] = auth.try_into().unwrap();
    (
        MerklePath::from_parts(position as u32, auth),
        node(leaves, DEPTH, 0).into(),
    )
}

fn read(bytes: &[u8]) -> Bundle<Authorized, ZatBalance> {
    read_v6_bundle(&mut Cursor::new(bytes), BranchId::Nu6_3, ValuePool::Ironwood)
        .unwrap()
        .unwrap()
}

fn leaves_of(bundle: &Bundle<Authorized, ZatBalance>) -> Vec<MerkleHashOrchard> {
    bundle
        .actions()
        .iter()
        .map(|a| MerkleHashOrchard::from_cmx(a.cmx()))
        .collect()
}

/// Builds, proves and signs one spend-and-pay bundle; returns its bytes and the change note.
fn build(
    pk: &ProvingKey,
    sk: &SpendingKey,
    spend: Note,
    tree: &[MerkleHashOrchard],
    position: u64,
    pay: u64,
    binding: Option<&[u8]>,
    name: &str,
    out: &PathBuf,
) -> (Vec<u8>, Option<Note>) {
    let mut rng = OsRng;
    let fvk = FullViewingKey::from(sk);
    let recipient = fvk.address_at(0u32, Scope::External);
    let (merkle_path, anchor) = path(tree, position);
    let mut builder = Builder::new(
        BundleType::UNPADDED,
        BundleVersion::ironwood_v3(),
        Flags::ENABLED,
        anchor,
    )
    .unwrap();
    builder.add_spend(fvk.clone(), spend, merkle_path).unwrap();
    if pay > 0 {
        builder
            .add_output(None, recipient, NoteValue::from_raw(pay), [0u8; 512])
            .unwrap();
    }
    let (unauthorized, _) = builder.build::<i64>(&mut rng).unwrap().unwrap();
    let ask = SpendAuthorizingKey::from(sk);
    let proven = unauthorized.create_proof(pk, &mut rng).unwrap();

    let to_zat = |v: i64| ZatBalance::from_i64(v).map_err(|_| ());
    let probe = proven
        .clone()
        .apply_signatures(&mut rng, [0u8; 32], &[ask.clone()])
        .unwrap()
        .try_map_value_balance::<_, (), _>(to_zat)
        .unwrap();
    let mut probe_bytes = Vec::new();
    write_v6_bundle(Some(&probe), &mut probe_bytes).unwrap();
    let hash = sighash(&probe_bytes, binding);

    let bundle = proven
        .apply_signatures(&mut rng, hash, &[ask])
        .unwrap()
        .try_map_value_balance::<_, (), _>(to_zat)
        .unwrap();
    let mut bytes = Vec::new();
    write_v6_bundle(Some(&bundle), &mut bytes).unwrap();
    assert_eq!(sighash(&bytes, binding), hash, "prefix must not depend on signatures");
    assert_eq!(orama_orchard::verify(&bytes, &hash), orama_orchard::OK);
    fs::write(out.join(format!("{name}.bundle")), &bytes).unwrap();
    fs::write(out.join(format!("{name}.sighash")), hash).unwrap();
    println!("{name}: {} bytes", bytes.len());

    let ivk = fvk.to_ivk(Scope::External);
    let change = (0..bundle.actions().len())
        .find_map(|i| bundle.decrypt_output_with_key(i, &ivk))
        .map(|(note, _, _)| note)
        .filter(|n| n.value().inner() == pay);
    (bytes, change)
}

fn main() {
    let out = PathBuf::from(std::env::args().nth(1).expect("output directory"));
    let signer = hex(2, "the unshields' signer");
    let validator = hex(3, "the validator the bond unshield delegates to");
    let sk = SpendingKey::from_bytes([7; 32]).unwrap();
    let fvk = FullViewingKey::from(&sk);
    let ivk = fvk.to_ivk(Scope::External);
    let pk = ProvingKey::build(OrchardCircuitVersion::PostNu6_3);

    let shield = read(&fs::read(out.join("ironwood-1-action.bundle")).unwrap());
    let note0 = (0..shield.actions().len())
        .find_map(|i| shield.decrypt_output_with_key(i, &ivk))
        .map(|(n, _, _)| n)
        .expect("the shielding bundle pays the wallet");
    assert_eq!(note0.value().inner(), 5000);

    let tree1 = leaves_of(&shield);
    let (transfer_bytes, change) = build(&pk, &sk, note0, &tree1, 0, 4900, None, "ironwood-transfer", &out);
    let change = change.expect("the transfer pays the wallet its change");

    let mut tree2 = tree1.clone();
    tree2.extend(leaves_of(&read(&transfer_bytes)));
    let position = tree2.len() as u64 - 1;
    let topup = binding(&signer, TARGET_FEE_TOPUP, &[]);
    build(&pk, &sk, change.clone(), &tree2, position, 0, Some(&topup), "ironwood-unshield", &out);
    let bond = binding(&signer, TARGET_BOND, &validator);
    build(&pk, &sk, change, &tree2, position, 0, Some(&bond), "ironwood-unshield-bond", &out);
}
