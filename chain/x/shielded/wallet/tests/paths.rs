//! `build_bundle_with_paths` (anchor + caller-supplied paths, for sparse-tree wallets) must produce
//! what `build_bundle` (a whole `NoteTree`) produces, and refuse paths that do not reach the anchor.

use orama_orchard::{verify, OK};
use orama_shielded_wallet::bundle::{
    build_bundle, build_bundle_with_paths, proving_key, scan, Built, BundleError, Output, Spend,
};
use orama_shielded_wallet::keys::Account;
use orama_shielded_wallet::sighash::sighash;
use orama_shielded_wallet::tree::NoteTree;
use orama_shielded_wallet::vectors::abandon_seed;
use orchard::keys::Scope;
use orchard::note::{ExtractedNoteCommitment, Note};
use orchard::tree::Anchor;
use rand::SeedableRng;
use rand_chacha::ChaCha20Rng;

const CHAIN: &str = "orama-test-paths";
const SEED: [u8; 32] = [7; 32];

struct Fixture {
    account: Account,
    tree: NoteTree,
    /// The shielded notes and their tree positions, in order.
    notes: Vec<(Note, u32)>,
}

fn addr(a: &Account, j: u32) -> orchard::Address {
    orchard::Address::from_raw_address_bytes(&a.address(j)).expect("address")
}

/// Shields two notes (1000 and 2000) to account 0 and appends them to a tree.
fn fixture() -> Fixture {
    let account = Account::derive(&abandon_seed(), 0).expect("account");
    let pk = proving_key();
    let mut rng = ChaCha20Rng::from_seed([1; 32]);
    let mut tree = NoteTree::new();
    let mut notes = Vec::new();
    for (j, value) in [(0u32, 1_000u64), (1, 2_000)] {
        let shield =
            build_bundle(&mut rng, &pk, CHAIN, None, &tree, &[], &[Output { recipient: addr(&account, j), value }]).expect("shield");
        let found = scan(&shield.bundle, &account.fvk.to_ivk(Scope::External));
        let (_, note) = found.into_iter().find(|(_, n)| n.value().inner() == value).expect("own note");
        let position = tree.append(&ExtractedNoteCommitment::from(note.commitment()));
        notes.push((note, position));
    }
    Fixture { account, tree, notes }
}

fn spend_of<'a>(f: &'a Fixture, i: usize) -> Spend<'a> {
    Spend { account: &f.account, note: f.notes[i].0, position: f.notes[i].1 }
}

fn pay(f: &Fixture, value: u64) -> Vec<Output> {
    vec![Output { recipient: addr(&f.account, 2), value }]
}

fn verifies(b: &Built) -> bool {
    verify(&b.bytes, &sighash(CHAIN, None, &b.bytes)) == OK
}

#[test]
fn explicit_paths_build_the_same_bundle_as_the_tree() {
    let f = fixture();
    let pk = proving_key();
    let outputs = pay(&f, 1_900);

    let via_tree =
        build_bundle(&mut ChaCha20Rng::from_seed(SEED), &pk, CHAIN, None, &f.tree, &[spend_of(&f, 1)], &outputs).expect("tree");
    let path = f.tree.witness(f.notes[1].1).expect("witness");
    let via_paths = build_bundle_with_paths(
        &mut ChaCha20Rng::from_seed(SEED),
        &pk,
        CHAIN,
        None,
        f.tree.root(),
        &[(spend_of(&f, 1), path)],
        &outputs,
    )
    .expect("paths");

    assert_eq!(via_paths.anchor, via_tree.anchor);
    assert_eq!(via_paths.anchor, f.tree.root().to_bytes());
    assert_eq!(via_paths.nullifiers, via_tree.nullifiers);
    assert_eq!(via_paths.bytes, via_tree.bytes, "same rng, same inputs: byte-identical bundles");
    assert_eq!(via_paths.value_balance, 100);
    assert!(verifies(&via_paths), "the chain's verifier accepts it");
    assert!(verifies(&via_tree));
}

#[test]
fn two_explicit_spends_verify() {
    let f = fixture();
    let pk = proving_key();
    let spends = [
        (spend_of(&f, 0), f.tree.witness(f.notes[0].1).unwrap()),
        (spend_of(&f, 1), f.tree.witness(f.notes[1].1).unwrap()),
    ];
    let built = build_bundle_with_paths(&mut ChaCha20Rng::from_seed(SEED), &pk, CHAIN, None, f.tree.root(), &spends, &pay(&f, 2_900))
        .expect("paths");
    assert_eq!(built.nullifiers.len(), 2);
    assert_eq!(built.value_balance, 100);
    assert!(verifies(&built));
}

#[test]
fn no_spends_with_the_empty_anchor_is_a_shield() {
    let f = fixture();
    let pk = proving_key();
    let built = build_bundle_with_paths(
        &mut ChaCha20Rng::from_seed(SEED),
        &pk,
        CHAIN,
        None,
        Anchor::empty_tree(),
        &[],
        &pay(&f, 500),
    )
    .expect("shield");
    assert_eq!(built.value_balance, -500);
    assert!(verifies(&built));
}

#[test]
fn a_path_for_another_leaf_or_another_anchor_is_refused() {
    let f = fixture();
    let pk = proving_key();
    let mut rng = ChaCha20Rng::from_seed(SEED);

    // The path of leaf 0 offered for the note at leaf 1.
    let wrong_leaf = f.tree.witness(f.notes[0].1).unwrap();
    let err = build_bundle_with_paths(&mut rng, &pk, CHAIN, None, f.tree.root(), &[(spend_of(&f, 1), wrong_leaf)], &pay(&f, 1))
        .err()
        .expect("refused");
    assert!(matches!(err, BundleError::BadPath(p) if p == f.notes[1].1), "{err:?}");

    // A correct path, but for an anchor from before the second note was appended.
    let mut older = NoteTree::new();
    older.append(&ExtractedNoteCommitment::from(f.notes[0].0.commitment()));
    let path = f.tree.witness(f.notes[1].1).unwrap();
    let err = build_bundle_with_paths(&mut rng, &pk, CHAIN, None, older.root(), &[(spend_of(&f, 1), path)], &pay(&f, 1))
        .err()
        .expect("refused");
    assert!(matches!(err, BundleError::BadPath(_)), "{err:?}");
}

#[test]
fn the_tree_wrapper_still_reports_an_unknown_position() {
    let f = fixture();
    let pk = proving_key();
    let ghost = Spend { account: &f.account, note: f.notes[0].0, position: 99 };
    let err = build_bundle(&mut ChaCha20Rng::from_seed(SEED), &pk, CHAIN, None, &f.tree, &[ghost], &pay(&f, 1))
        .err()
        .expect("refused");
    assert!(matches!(err, BundleError::UnknownPosition(99)), "{err:?}");
}
