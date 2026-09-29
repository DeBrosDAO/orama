//! Every bundle the builder makes must pass the chain's verifier, and tampered ones must not.

use std::sync::OnceLock;

use orama_orchard::{verify, MALFORMED, OK, PROOF_REJECTED, SIGNATURE_REJECTED};
use orama_shielded_wallet::scenario::{self, Scenario, Step};
use orama_shielded_wallet::sighash::{sighash, ACTION_LEN, HEADER_LEN};
use orama_shielded_wallet::tree::NoteTree;
use orchard::note::ExtractedNoteCommitment;

const COMMITTED: &str = include_str!("../testdata/bundles/scenario.json");

fn committed() -> &'static Scenario {
    static S: OnceLock<Scenario> = OnceLock::new();
    S.get_or_init(|| serde_json::from_str(COMMITTED).expect("scenario.json parses"))
}

fn bytes(h: &str) -> Vec<u8> {
    hex::decode(h).expect("hex")
}

fn hash(step: &Step) -> [u8; 32] {
    bytes(&step.sighash).try_into().unwrap()
}

fn flipped(b: &[u8], at: usize) -> Vec<u8> {
    let mut c = b.to_vec();
    c[at] ^= 1;
    c
}

#[test]
fn committed_scenario_has_shields_a_transfer_and_an_unshield_with_real_spends() {
    let kinds: Vec<_> = committed().steps.iter().map(|s| (s.kind.as_str(), s.real_spends, s.value_balance)).collect();
    assert_eq!(
        kinds,
        [("shield", 0, -10_000), ("shield", 0, -7_000), ("transfer", 1, 0), ("unshield", 1, 5_000)]
    );
}

#[test]
fn every_committed_bundle_passes_the_chain_verifier() {
    for s in &committed().steps {
        assert_eq!(verify(&bytes(&s.bundle), &hash(s)), OK, "{}", s.name);
    }
}

#[test]
fn sighash_matches_the_chain_definition_over_the_bundle_bytes() {
    // The Go tests recompute the same value with orchard.Sighash; both must equal the stored hash.
    for s in &committed().steps {
        let b = bytes(&s.bundle);
        assert_eq!(sighash(&committed().chain_id, &b).to_vec(), bytes(&s.sighash), "{}", s.name);
        assert!(b.starts_with(&bytes(&s.effecting_data)), "{}", s.name);
    }
}

#[test]
fn tampering_is_rejected() {
    for s in &committed().steps {
        let b = bytes(&s.bundle);
        let n = b[0] as usize;
        let proof_at = 1 + n * ACTION_LEN + HEADER_LEN + 3;
        let sigs_at = proof_at + 2720 + 2272 * n;
        let cases = [
            ("proof", proof_at + 200, PROOF_REJECTED),
            ("ciphertext", 1 + 32 * 5 + 100, SIGNATURE_REJECTED),
            ("value balance", 1 + n * ACTION_LEN + 1 + 2, SIGNATURE_REJECTED),
            ("spend-auth signature", sigs_at + 5, SIGNATURE_REJECTED),
            ("binding signature", b.len() - 10, SIGNATURE_REJECTED),
        ];
        for (what, at, want) in cases {
            // The chain recomputes the sighash from the bytes it receives, so do the same.
            let mutated = flipped(&b, at);
            let h = sighash(&committed().chain_id, &mutated);
            assert_eq!(verify(&mutated, &h), want, "{} / {what}", s.name);
        }
        let mut bad_hash = hash(s);
        bad_hash[0] ^= 1;
        assert_eq!(verify(&b, &bad_hash), SIGNATURE_REJECTED, "{} / sighash", s.name);
        assert_ne!(verify(&b[..b.len() - 1], &hash(s)), OK, "{} / truncated", s.name);
        assert_eq!(verify(&[], &hash(s)), MALFORMED, "{} / empty", s.name);
    }
}

#[test]
fn a_bundle_for_another_chain_id_is_rejected() {
    let s = &committed().steps[2];
    let other = sighash("some-other-orama-chain", &bytes(&s.bundle));
    assert_eq!(verify(&bytes(&s.bundle), &other), SIGNATURE_REJECTED);
}

#[test]
fn anchors_follow_the_note_tree_and_nullifiers_are_unique() {
    let mut tree = NoteTree::new();
    let mut seen = std::collections::HashSet::new();
    for s in &committed().steps {
        assert_eq!(hex::encode(tree.root().to_bytes()), s.anchor, "{}: anchor is the root before it", s.name);
        assert_eq!(tree.len() as u32, s.first_position, "{}", s.name);
        for nf in &s.nullifiers {
            assert!(seen.insert(nf.clone()), "{}: nullifier repeated", s.name);
        }
        for c in &s.cmxs {
            let cmx: [u8; 32] = bytes(c).try_into().unwrap();
            let cmx: ExtractedNoteCommitment = Option::from(ExtractedNoteCommitment::from_bytes(&cmx)).unwrap();
            tree.append(&cmx);
        }
    }
}

#[test]
fn regenerating_the_scenario_reproduces_the_committed_vectors() {
    // Deterministic RNG: same code, same bytes. A different orchard version would change this.
    let fresh = scenario::run().expect("scenario builds");
    assert_eq!(&fresh, committed());
}

#[test]
fn spending_a_note_that_is_not_in_the_tree_is_refused() {
    use orama_shielded_wallet::bundle::{build_bundle, proving_key, scan, BundleError, Output, Spend};
    use orama_shielded_wallet::keys::Account;
    use orama_shielded_wallet::vectors::abandon_seed;
    use orchard::keys::Scope;
    use rand::SeedableRng;

    let a = Account::derive(&abandon_seed(), 0).unwrap();
    let addr = orchard::Address::from_raw_address_bytes(&a.address(0)).unwrap();
    let mut rng = rand_chacha::ChaCha20Rng::from_seed([1; 32]);
    let pk = proving_key();
    let empty = NoteTree::new();

    let shield = build_bundle(&mut rng, &pk, "test", &empty, &[], &[Output { recipient: addr, value: 500 }]).unwrap();
    let notes = scan(&shield.bundle, &a.fvk.to_ivk(Scope::External));
    let (_, note) = notes.into_iter().find(|(_, n)| n.value().inner() == 500).unwrap();

    let err = build_bundle(
        &mut rng,
        &pk,
        "test",
        &empty,
        &[Spend { account: &a, note, position: 0 }],
        &[Output { recipient: addr, value: 500 }],
    )
    .err()
    .expect("must fail");
    assert!(matches!(err, BundleError::UnknownPosition(0)), "{err:?}");

    let err = build_bundle(&mut rng, &pk, "test", &empty, &[], &[]).err().expect("must fail");
    assert!(matches!(err, BundleError::Build(_) | BundleError::Empty), "{err:?}");
}
