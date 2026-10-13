//! The load generator's bundle factory: what it builds must pass the chain's verifier, and what it
//! refuses must be refused for the right reason.

use orama_orchard::{verify, OK};
use orama_shielded_wallet::notes::{decode_note, encode_note};
use orama_shielded_wallet::bench::{run, OutputSpec, Request, ShieldJob, SpendJob, SpendRef};
use orama_shielded_wallet::sighash::{sighash, unshield_binding, Target};

const CHAIN: &str = "orama-localnet-bench-1";
const SIGNER: [u8; 20] = [7; 20];

fn out(account: u32, address: u32, value: u64) -> OutputSpec {
    OutputSpec { account, address, value }
}

fn bytes(h: &str) -> Vec<u8> {
    hex::decode(h).expect("hex")
}

/// One shield of two notes for wallet 0, then a transfer and an unshield spending them.
#[test]
fn shielded_notes_can_be_spent_by_a_transfer_and_an_unshield_that_the_chain_verifies() {
    let shield = run(
        Request::Shields {
            chain_id: CHAIN.into(),
            jobs: vec![ShieldJob { id: "seed".into(), outputs: vec![out(0, 0, 10_000), out(0, 0, 10_000)] }],
        },
        |_, _, _| {},
    )
    .expect("shield builds");
    let seed = &shield.built[0];
    assert_eq!(seed.value_balance, -20_000);
    assert_eq!(seed.notes.len(), 2, "both outputs belong to wallet 0 and must be found by scanning");
    assert_eq!(seed.anchor, hex::encode(orchard::Anchor::empty_tree().to_bytes()), "a shield spends nothing real, so it anchors to the empty tree");
    assert_eq!(verify(&bytes(&seed.bundle), &sighash(CHAIN, None, &bytes(&seed.bundle))), OK);

    // The chain's tree after the shield holds the bundle's commitments in action order.
    let leaves = seed.cmxs.clone();
    let spends = |note: &str| vec![SpendRef { account: 0, note: note.into() }];
    let resp = run(
        Request::Spends {
            chain_id: CHAIN.into(),
            leaves: leaves.clone(),
            expect_root: None,
            jobs: vec![
                SpendJob { id: "transfer".into(), kind: "transfer".into(), spends: spends(&seed.notes[0].note), outputs: vec![out(1, 0, 9_000)], unshield_signer: None },
                SpendJob {
                    id: "unshield".into(),
                    kind: "unshield".into(),
                    spends: spends(&seed.notes[1].note),
                    outputs: vec![out(0, 1, 4_000)],
                    unshield_signer: Some(hex::encode(SIGNER)),
                },
            ],
        },
        |_, _, _| {},
    )
    .expect("spends build");
    let (transfer, unshield) = (&resp.built[0], &resp.built[1]);
    assert_eq!(transfer.value_balance, 1_000, "a transfer leaves what it spends less what it pays as its fee");
    assert_eq!(unshield.value_balance, 6_000);
    assert_eq!(transfer.anchor, resp.anchor);
    assert_ne!(resp.anchor, seed.anchor);
    assert_eq!(verify(&bytes(&transfer.bundle), &sighash(CHAIN, None, &bytes(&transfer.bundle))), OK);
    let binding = unshield_binding(&SIGNER, Target::FeeTopup);
    assert_eq!(verify(&bytes(&unshield.bundle), &sighash(CHAIN, Some(&binding), &bytes(&unshield.bundle))), OK);
    // The nullifiers of the two spends differ: each note is spendable once.
    assert_ne!(transfer.nullifiers, unshield.nullifiers);
}

#[test]
fn a_note_survives_serialization_and_a_corrupt_one_is_refused() {
    let shield = run(
        Request::Shields { chain_id: CHAIN.into(), jobs: vec![ShieldJob { id: "n".into(), outputs: vec![out(2, 3, 777)] }] },
        |_, _, _| {},
    )
    .unwrap();
    let hex_note = &shield.built[0].notes[0].note;
    let note = decode_note(hex_note).expect("decodes");
    assert_eq!(note.value().inner(), 777);
    assert_eq!(&encode_note(&note), hex_note, "encode and decode are inverses");

    assert!(decode_note("zz").is_err(), "not hex");
    assert!(decode_note(&hex_note[..hex_note.len() - 2]).is_err(), "short");
    let mut bad = bytes(hex_note);
    *bad.last_mut().unwrap() = 9;
    assert!(decode_note(&hex::encode(bad)).is_err(), "unknown version");
}

#[test]
fn spends_are_refused_when_the_tree_or_the_note_does_not_match() {
    let shield = run(
        Request::Shields { chain_id: CHAIN.into(), jobs: vec![ShieldJob { id: "s".into(), outputs: vec![out(0, 0, 5_000)] }] },
        |_, _, _| {},
    )
    .unwrap();
    let seed = &shield.built[0];
    let job = |note: &str| SpendJob {
        id: "j".into(),
        kind: "transfer".into(),
        spends: vec![SpendRef { account: 0, note: note.into() }],
        outputs: vec![out(1, 0, 4_000)],
        unshield_signer: None,
    };
    // A root that is not the leaves' root: the leaf list is not the chain's tree.
    let err = run(
        Request::Spends { chain_id: CHAIN.into(), leaves: seed.cmxs.clone(), expect_root: Some("00".repeat(32)), jobs: vec![job(&seed.notes[0].note)] },
        |_, _, _| {},
    )
    .err()
    .expect("a wrong root is refused");
    assert!(err.contains("not the chain's tree"), "{err}");
    // A note whose commitment is not among the leaves.
    let err = run(
        Request::Spends { chain_id: CHAIN.into(), leaves: vec![], expect_root: None, jobs: vec![job(&seed.notes[0].note)] },
        |_, _, _| {},
    )
    .err()
    .expect("a note absent from the tree is refused");
    assert!(err.contains("not in the tree"), "{err}");
    // An unshield with no signer, and a transfer with one.
    let mut no_signer = job(&seed.notes[0].note);
    no_signer.kind = "unshield".into();
    let err = run(Request::Spends { chain_id: CHAIN.into(), leaves: seed.cmxs.clone(), expect_root: None, jobs: vec![no_signer] }, |_, _, _| {}).err().unwrap();
    assert!(err.contains("unshield_signer"), "{err}");
    let mut stray = job(&seed.notes[0].note);
    stray.unshield_signer = Some(hex::encode(SIGNER));
    assert!(run(Request::Spends { chain_id: CHAIN.into(), leaves: seed.cmxs.clone(), expect_root: None, jobs: vec![stray] }, |_, _, _| {}).is_err());
}
