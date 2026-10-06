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

/// The fee the committed scenario's transfer leaves in its value balance.
pub const DEFAULT_FEE: u64 = 100;

/// The knobs a live chain needs and the committed scenario does not: its chain ID, the address the
/// unshield is signed for, a multiplier on every amount (so the amounts clear the chain's real
/// unshield floor and fees) and the fee the transfer leaves. `Config::default()` is the committed
/// scenario exactly.
#[derive(Clone, Debug)]
pub struct Config {
    pub chain_id: String,
    pub unshield_signer: [u8; 20],
    /// Multiplies the shield, transfer and unshield amounts. The fee is not multiplied.
    pub scale: u64,
    /// What the transfer leaves in its value balance, in base units.
    pub fee: u64,
}

impl Default for Config {
    fn default() -> Self {
        Config { chain_id: CHAIN_ID.into(), unshield_signer: UNSHIELD_SIGNER, scale: 1, fee: DEFAULT_FEE }
    }
}

impl Config {
    /// Reads `ORAMA_SCENARIO_CHAIN_ID`, `ORAMA_SCENARIO_UNSHIELD_SIGNER` (40 hex digits),
    /// `ORAMA_SCENARIO_SCALE` and `ORAMA_SCENARIO_FEE` from `get`; a variable that is not set keeps
    /// the committed scenario's value, and one that is set but wrong is an error.
    pub fn from_env(get: impl Fn(&str) -> Option<String>) -> Result<Config, String> {
        let mut cfg = Config::default();
        if let Some(v) = get("ORAMA_SCENARIO_CHAIN_ID") {
            if v.is_empty() {
                return Err("ORAMA_SCENARIO_CHAIN_ID is empty".into());
            }
            cfg.chain_id = v;
        }
        if let Some(v) = get("ORAMA_SCENARIO_UNSHIELD_SIGNER") {
            let raw = unhex(&v)?;
            cfg.unshield_signer = raw.try_into().map_err(|_| "ORAMA_SCENARIO_UNSHIELD_SIGNER must be 20 bytes (40 hex digits)".to_string())?;
        }
        if let Some(v) = get("ORAMA_SCENARIO_SCALE") {
            cfg.scale = v.parse::<u64>().map_err(|_| "ORAMA_SCENARIO_SCALE is not an integer".to_string())?;
            if cfg.scale == 0 {
                return Err("ORAMA_SCENARIO_SCALE must be at least 1".into());
            }
        }
        if let Some(v) = get("ORAMA_SCENARIO_FEE") {
            cfg.fee = v.parse::<u64>().map_err(|_| "ORAMA_SCENARIO_FEE is not an integer".to_string())?;
        }
        Ok(cfg)
    }
}

fn unhex(s: &str) -> Result<Vec<u8>, String> {
    if s.len() % 2 != 0 || !s.bytes().all(|b| b.is_ascii_hexdigit()) {
        return Err(format!("{s:?} is not hex"));
    }
    (0..s.len()).step_by(2).map(|i| u8::from_str_radix(&s[i..i + 2], 16).map_err(|e| e.to_string())).collect()
}

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
    run_with(&Config::default())
}

/// The scenario for a live chain: `run()` with `cfg`'s chain ID, unshield signer, amounts and fee.
/// The steps and their order are the committed scenario's, with every amount multiplied by
/// `cfg.scale`. The transfer spends the first shield's note, sends 6,000 x scale to account 1 and
/// returns the rest less `cfg.fee` as change, so its value balance is the fee.
pub fn run_with(cfg: &Config) -> Result<Scenario, BundleError> {
    let chain_id = cfg.chain_id.as_str();
    let s = cfg.scale;
    assert!(cfg.fee < 4 * s * 1_000, "the fee must be below the transfer's change");
    let (v10, v7, v6, v1) = (10_000 * s, 7_000 * s, 6_000 * s, 1_000 * s);
    let change = v10 - v6 - cfg.fee;
    let seed = abandon_seed();
    let a0 = Account::derive(&seed, 0).expect("account 0");
    let a1 = Account::derive(&seed, 1).expect("account 1");
    let mut rng = ChaCha20Rng::from_seed(RNG_SEED);
    let pk = proving_key();
    let mut tree = NoteTree::new();
    let mut steps = Vec::new();
    let addr = |a: &Account, j: u32| orchard::Address::from_raw_address_bytes(&a.address(j)).unwrap();

    let shield1 = build_bundle(&mut rng, &pk, chain_id, None, &tree, &[], &[Output { recipient: addr(&a0, 0), value: v10 }])?;
    let (first, notes) = apply(&mut tree, &shield1, &a0);
    steps.push(step("shield-1", "shield", &shield1, &[], first, 0));
    let note10k = only(notes, v10);

    let shield2 = build_bundle(&mut rng, &pk, chain_id, None, &tree, &[], &[Output { recipient: addr(&a0, 1), value: v7 }])?;
    let (first, _) = apply(&mut tree, &shield2, &a0);
    steps.push(step("shield-2", "shield", &shield2, &[], first, 0));

    let transfer = build_bundle(
        &mut rng,
        &pk,
        chain_id,
        None,
        &tree,
        &[Spend { account: &a0, note: note10k.note, position: note10k.position }],
        &[
            Output { recipient: addr(&a1, 0), value: v6 },
            Output { recipient: addr(&a0, 2), value: change },
        ],
    )?;
    let (first, notes) = apply(&mut tree, &transfer, &a1);
    steps.push(step("transfer", "transfer", &transfer, &[], first, 1));
    let note6k = only(notes, v6);

    let binding = unshield_binding(&cfg.unshield_signer, Target::FeeTopup);
    let unshield = build_bundle(
        &mut rng,
        &pk,
        chain_id,
        Some(&binding),
        &tree,
        &[Spend { account: &a1, note: note6k.note, position: note6k.position }],
        &[Output { recipient: addr(&a1, 1), value: v1 }],
    )?;
    let (first, _) = apply(&mut tree, &unshield, &a1);
    steps.push(step("unshield", "unshield", &unshield, &binding, first, 1));

    Ok(Scenario {
        chain_id: chain_id.into(),
        description: "all-abandon seed, accounts 0 and 1: two shields, a transfer that spends a real note and pays a fee of 100, an unshield bound to its signer and target that spends a real note".into(),
        steps,
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::collections::HashMap;

    fn env(pairs: &[(&str, &str)]) -> impl Fn(&str) -> Option<String> {
        let map: HashMap<String, String> = pairs.iter().map(|(k, v)| (k.to_string(), v.to_string())).collect();
        move |k| map.get(k).cloned()
    }

    #[test]
    fn config_with_no_variables_is_the_committed_scenario() {
        let cfg = Config::from_env(env(&[])).unwrap();
        assert_eq!(cfg.chain_id, CHAIN_ID);
        assert_eq!(cfg.unshield_signer, UNSHIELD_SIGNER);
        assert_eq!((cfg.scale, cfg.fee), (1, DEFAULT_FEE));
    }

    #[test]
    fn config_reads_every_variable() {
        let signer = "00112233445566778899aabbccddeeff00112233";
        let cfg = Config::from_env(env(&[
            ("ORAMA_SCENARIO_CHAIN_ID", "orama-stagenet-1"),
            ("ORAMA_SCENARIO_UNSHIELD_SIGNER", signer),
            ("ORAMA_SCENARIO_SCALE", "1000000"),
            ("ORAMA_SCENARIO_FEE", "500000"),
        ]))
        .unwrap();
        assert_eq!(cfg.chain_id, "orama-stagenet-1");
        assert_eq!(cfg.unshield_signer[0], 0x00);
        assert_eq!(cfg.unshield_signer[19], 0x33);
        assert_eq!((cfg.scale, cfg.fee), (1_000_000, 500_000));
    }

    #[test]
    fn config_refuses_a_wrong_value_instead_of_falling_back() {
        for pair in [
            ("ORAMA_SCENARIO_CHAIN_ID", ""),
            ("ORAMA_SCENARIO_UNSHIELD_SIGNER", "abcd"),
            ("ORAMA_SCENARIO_UNSHIELD_SIGNER", "zz112233445566778899aabbccddeeff00112233"),
            ("ORAMA_SCENARIO_SCALE", "0"),
            ("ORAMA_SCENARIO_SCALE", "many"),
            ("ORAMA_SCENARIO_FEE", "-1"),
        ] {
            assert!(Config::from_env(env(&[pair])).is_err(), "{pair:?}");
        }
    }
}
