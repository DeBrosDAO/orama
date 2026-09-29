//! The committed bundle scenario: two accounts of the all-"abandon" seed move value through the
//! pool with real spends. Generation is deterministic (seeded ChaCha20), so regenerating must
//! reproduce `testdata/bundles/scenario.json` byte for byte.
//!
//! 1. shield  10000 to account 0, address 0   (transparent -> shielded, dummy spends only)
//! 2. shield   7000 to account 0, address 1
//! 3. transfer: account 0 spends the 10000 note; 6000 to account 1, 3900 change to account 0, and
//!    100 left as the fee (the value balance), which the chain requires of a transfer
//! 4. unshield: account 1 spends the 6000 note; 1000 change to account 1, 5000 leaves the pool for
//!    the fee-only balance of [`UNSHIELD_SIGNER`], and the bundle is signed for that signer and target
//!
//! The chain ID is a localnet one so the chain's locked genesis parameters do not apply, and the
//! amounts are small: an app test sets action gas 10 and a nullifier fee of 1, so a two-action
//! transfer needs a fee of 22 and the 100 left covers it.

use orchard::keys::Scope;
use orchard::note::Note;
use rand::SeedableRng;
use rand_chacha::ChaCha20Rng;
use serde::{Deserialize, Serialize};

use crate::bundle::{build_bundle, proving_key, scan, Built, BundleError, Output, Spend};
use crate::sighash::{unshield_binding, Target};
use crate::keys::Account;
use crate::tree::NoteTree;
use crate::vectors::{abandon_seed, hex};

pub const CHAIN_ID: &str = "orama-localnet-shielded-wallet-1";
/// The address the unshield is signed for: the test account the chain's app tests derive with
/// `GenPrivKeyFromSecret("orama-shielded-test-alice")` (chain/app/shielded_keys_test.go pins it).
pub const UNSHIELD_SIGNER: [u8; 20] = [
    0x5f, 0x64, 0xe1, 0x12, 0x8a, 0xfd, 0xe9, 0x34, 0xc7, 0x92, 0x93, 0x9c, 0x8e, 0x64, 0xfc, 0xa4, 0x6b, 0x7f, 0x71, 0xf6,
];
const RNG_SEED: [u8; 32] = *b"orama-shielded-wallet-scenario-1";

#[derive(Serialize, Deserialize, PartialEq, Eq, Debug)]
pub struct Step {
    pub name: String,
    pub kind: String,
    pub bundle: String,
    pub effecting_data: String,
    pub sighash: String,
    pub anchor: String,
    /// Public value balance in base units: negative shields, positive is the fee of a transfer or
    /// what an unshield takes out.
    pub value_balance: i64,
    /// Hex of the sighash binding (signer and target), empty for a bundle that has none.
    pub binding: String,
    pub nullifiers: Vec<String>,
    pub cmxs: Vec<String>,
    /// Tree position of the first cmx of this bundle; the rest follow in action order.
    pub first_position: u32,
    /// Number of spends that are real (not padding dummies).
    pub real_spends: u32,
}

#[derive(Serialize, Deserialize, PartialEq, Eq, Debug)]
pub struct Scenario {
    pub chain_id: String,
    pub description: String,
    pub steps: Vec<Step>,
}

fn step(name: &str, kind: &str, b: &Built, binding: &[u8], first_position: u32, real_spends: u32) -> Step {
    Step {
        name: name.into(),
        kind: kind.into(),
        bundle: hex(&b.bytes),
        effecting_data: hex(&b.effecting_data),
        sighash: hex(&b.sighash),
        anchor: hex(&b.anchor),
        value_balance: b.value_balance,
        binding: hex(binding),
        nullifiers: b.nullifiers.iter().map(|n| hex(n)).collect(),
        cmxs: b.cmxs.iter().map(|c| hex(c)).collect(),
        first_position,
        real_spends,
    }
}

/// A note the wallet found by scanning, with its tree position.
struct Owned {
    note: Note,
    position: u32,
}

/// Appends a bundle's commitments to the tree and returns the notes `account` can read.
fn apply(tree: &mut NoteTree, b: &Built, account: &Account) -> (u32, Vec<Owned>) {
    let first = tree.len() as u32;
    for a in b.bundle.actions() {
        tree.append(a.cmx());
    }
    let ivk = account.fvk.to_ivk(Scope::External);
    let owned = scan(&b.bundle, &ivk)
        .into_iter()
        .map(|(idx, note)| Owned { note, position: first + idx as u32 })
        .collect();
    (first, owned)
}

fn only(mut notes: Vec<Owned>, value: u64) -> Owned {
    notes.retain(|o| o.note.value().inner() == value);
    assert_eq!(notes.len(), 1, "exactly one note of {value}");
    notes.remove(0)
}

pub fn run() -> Result<Scenario, BundleError> {
    let seed = abandon_seed();
    let a0 = Account::derive(&seed, 0).expect("account 0");
    let a1 = Account::derive(&seed, 1).expect("account 1");
    let mut rng = ChaCha20Rng::from_seed(RNG_SEED);
    let pk = proving_key();
    let mut tree = NoteTree::new();
    let mut steps = Vec::new();
    let addr = |a: &Account, j: u32| orchard::Address::from_raw_address_bytes(&a.address(j)).unwrap();

    let shield1 = build_bundle(&mut rng, &pk, CHAIN_ID, None, &tree, &[], &[Output { recipient: addr(&a0, 0), value: 10_000 }])?;
    let (first, notes) = apply(&mut tree, &shield1, &a0);
    steps.push(step("shield-1", "shield", &shield1, &[], first, 0));
    let note10k = only(notes, 10_000);

    let shield2 = build_bundle(&mut rng, &pk, CHAIN_ID, None, &tree, &[], &[Output { recipient: addr(&a0, 1), value: 7_000 }])?;
    let (first, _) = apply(&mut tree, &shield2, &a0);
    steps.push(step("shield-2", "shield", &shield2, &[], first, 0));

    let transfer = build_bundle(
        &mut rng,
        &pk,
        CHAIN_ID,
        None,
        &tree,
        &[Spend { account: &a0, note: note10k.note, position: note10k.position }],
        &[
            Output { recipient: addr(&a1, 0), value: 6_000 },
            Output { recipient: addr(&a0, 2), value: 3_900 },
        ],
    )?;
    let (first, notes) = apply(&mut tree, &transfer, &a1);
    steps.push(step("transfer", "transfer", &transfer, &[], first, 1));
    let note6k = only(notes, 6_000);

    let binding = unshield_binding(&UNSHIELD_SIGNER, Target::FeeTopup);
    let unshield = build_bundle(
        &mut rng,
        &pk,
        CHAIN_ID,
        Some(&binding),
        &tree,
        &[Spend { account: &a1, note: note6k.note, position: note6k.position }],
        &[Output { recipient: addr(&a1, 1), value: 1_000 }],
    )?;
    let (first, _) = apply(&mut tree, &unshield, &a1);
    steps.push(step("unshield", "unshield", &unshield, &binding, first, 1));

    Ok(Scenario {
        chain_id: CHAIN_ID.into(),
        description: "all-abandon seed, accounts 0 and 1: two shields, a transfer that spends a real note and pays a fee of 100, an unshield bound to its signer and target that spends a real note".into(),
        steps,
    })
}
