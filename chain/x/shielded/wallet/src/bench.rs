//! The bundle factory of the load generator (`chain/cmd/oramad-bench`). It proves bundles off-node,
//! as every wallet does, and hands them to the generator as JSON.
//!
//! Two requests, because a spend can only be proved against notes the chain already holds:
//!
//! * `shields`: bundles that put value into the pool. They spend nothing real, so they are anchored
//!   to the empty tree and stay valid; each output note comes back serialized so a later request
//!   can spend it.
//! * `spends`: transfers and unshields that spend those notes. The request carries the chain's
//!   note commitments in tree order; the factory builds the tree, checks its root against the
//!   chain's, finds each note's position by its commitment, and anchors every bundle to that root.
//!
//! The note codec moved to [`crate::notes`]; `bench::encode_note` / `bench::decode_note` remain as
//! re-exports for existing callers.
//!
//! The accounts are those of the public all-"abandon" test seed: a localnet load generator needs
//! reproducible wallets, not secret ones.

use std::collections::HashMap;

use orchard::circuit::ProvingKey;
use orchard::keys::Scope;
use orchard::note::{ExtractedNoteCommitment, Note};
use rand::SeedableRng;
use rand_chacha::ChaCha20Rng;
use serde::{Deserialize, Serialize};

use crate::bundle::{build_bundle, proving_key, scan, Built, Output, Spend};
use crate::keys::Account;
use crate::sighash::{unshield_binding, Target};
use crate::tree::NoteTree;
use crate::vectors::{abandon_seed, hex};

pub use crate::notes::{decode_note, encode_note};

const SIGNER_LEN: usize = 20;

#[derive(Deserialize)]
pub struct OutputSpec {
    pub account: u32,
    pub address: u32,
    pub value: u64,
}

#[derive(Deserialize)]
pub struct ShieldJob {
    pub id: String,
    pub outputs: Vec<OutputSpec>,
}

#[derive(Deserialize)]
pub struct SpendRef {
    pub account: u32,
    pub note: String,
}

#[derive(Deserialize)]
pub struct SpendJob {
    pub id: String,
    /// "transfer" or "unshield".
    pub kind: String,
    pub spends: Vec<SpendRef>,
    pub outputs: Vec<OutputSpec>,
    /// 20 address bytes in hex: the account an unshield is signed for (its target is the fee top-up).
    pub unshield_signer: Option<String>,
}

#[derive(Deserialize)]
#[serde(tag = "mode", rename_all = "snake_case")]
pub enum Request {
    Shields { chain_id: String, jobs: Vec<ShieldJob> },
    Spends { chain_id: String, leaves: Vec<String>, expect_root: Option<String>, jobs: Vec<SpendJob> },
}

#[derive(Serialize)]
pub struct NoteOut {
    pub account: u32,
    pub value: u64,
    pub action: usize,
    pub cmx: String,
    pub note: String,
}

#[derive(Serialize)]
pub struct BuiltOut {
    pub id: String,
    pub bundle: String,
    pub actions: usize,
    pub value_balance: i64,
    pub anchor: String,
    pub nullifiers: Vec<String>,
    pub cmxs: Vec<String>,
    pub notes: Vec<NoteOut>,
}

#[derive(Serialize)]
pub struct Response {
    pub built: Vec<BuiltOut>,
    /// The root the spends were anchored to (empty for a shields request).
    pub anchor: String,
}

/// Wallet accounts of the test seed, derived once each.
struct Wallet {
    seed: [u8; 64],
    accounts: HashMap<u32, Account>,
}

impl Wallet {
    fn new() -> Self {
        Wallet { seed: abandon_seed(), accounts: HashMap::new() }
    }

    fn account(&mut self, index: u32) -> Result<&Account, String> {
        if !self.accounts.contains_key(&index) {
            let acct = Account::derive(&self.seed, index).map_err(|e| format!("derive account {index}: {e:?}"))?;
            self.accounts.insert(index, acct);
        }
        Ok(&self.accounts[&index])
    }

    fn outputs(&mut self, specs: &[OutputSpec]) -> Result<Vec<Output>, String> {
        specs
            .iter()
            .map(|o| {
                let raw = self.account(o.account)?.address(o.address);
                let recipient = Option::from(orchard::Address::from_raw_address_bytes(&raw))
                    .ok_or_else(|| format!("account {} address {} is not an Orchard address", o.account, o.address))?;
                Ok(Output { recipient, value: o.value })
            })
            .collect()
    }
}

fn cmx_hex(n: &Note) -> String {
    hex(&ExtractedNoteCommitment::from(n.commitment()).to_bytes())
}

fn built_out(id: &str, b: &Built, notes: Vec<NoteOut>) -> BuiltOut {
    BuiltOut {
        id: id.to_string(),
        bundle: hex(&b.bytes),
        actions: b.cmxs.len(),
        value_balance: b.value_balance,
        anchor: hex(&b.anchor),
        nullifiers: b.nullifiers.iter().map(|n| hex(n)).collect(),
        cmxs: b.cmxs.iter().map(|c| hex(c)).collect(),
        notes,
    }
}

fn shield(w: &mut Wallet, rng: &mut ChaCha20Rng, pk: &ProvingKey, chain_id: &str, job: &ShieldJob) -> Result<BuiltOut, String> {
    let outputs = w.outputs(&job.outputs)?;
    let tree = NoteTree::new();
    let built = build_bundle(rng, pk, chain_id, None, &tree, &[], &outputs).map_err(|e| format!("shield {}: {e}", job.id))?;
    let mut notes = Vec::new();
    let mut accounts: Vec<u32> = job.outputs.iter().map(|o| o.account).collect();
    accounts.sort_unstable();
    accounts.dedup();
    for acct in accounts {
        let ivk = w.account(acct)?.fvk.to_ivk(Scope::External);
        for (action, note) in scan(&built.bundle, &ivk) {
            notes.push(NoteOut { account: acct, value: note.value().inner(), action, cmx: cmx_hex(&note), note: encode_note(&note) });
        }
    }
    Ok(built_out(&job.id, &built, notes))
}

fn note_tree(leaves: &[String], expect_root: &Option<String>) -> Result<(NoteTree, HashMap<String, u32>), String> {
    let mut tree = NoteTree::new();
    let mut positions = HashMap::with_capacity(leaves.len());
    for (i, leaf) in leaves.iter().enumerate() {
        let raw = hex::decode(leaf).map_err(|e| format!("leaf {i} is not hex: {e}"))?;
        let bytes: [u8; 32] = raw.try_into().map_err(|_| format!("leaf {i} is not 32 bytes"))?;
        let cmx = Option::from(ExtractedNoteCommitment::from_bytes(&bytes)).ok_or_else(|| format!("leaf {i} is not a note commitment"))?;
        positions.insert(leaf.clone(), tree.append(&cmx));
    }
    if let Some(want) = expect_root {
        let got = hex(&tree.root().to_bytes());
        if &got != want {
            return Err(format!("the {} leaves root to {got}, the chain's tree root is {want}: the leaf list is not the chain's tree", leaves.len()));
        }
    }
    Ok((tree, positions))
}

fn spend_job(
    w: &mut Wallet,
    rng: &mut ChaCha20Rng,
    pk: &ProvingKey,
    chain_id: &str,
    tree: &NoteTree,
    positions: &HashMap<String, u32>,
    job: &SpendJob,
) -> Result<BuiltOut, String> {
    let mut owned = Vec::new();
    for s in &job.spends {
        let note = decode_note(&s.note)?;
        let position = *positions.get(&cmx_hex(&note)).ok_or_else(|| format!("job {}: a spent note is not in the tree", job.id))?;
        owned.push((s.account, note, position));
    }
    for (account, _, _) in &owned {
        w.account(*account)?;
    }
    let spends: Vec<Spend> = owned.iter().map(|(a, n, p)| Spend { account: &w.accounts[a], note: *n, position: *p }).collect();
    let binding = match (job.kind.as_str(), &job.unshield_signer) {
        ("transfer", None) => None,
        ("unshield", Some(signer)) => {
            let raw = hex::decode(signer).map_err(|e| format!("job {}: unshield_signer is not hex: {e}", job.id))?;
            if raw.len() != SIGNER_LEN {
                return Err(format!("job {}: unshield_signer is {} bytes, want {SIGNER_LEN}", job.id, raw.len()));
            }
            Some(unshield_binding(&raw, Target::FeeTopup))
        }
        (kind, _) => return Err(format!("job {}: kind {kind:?} does not match its unshield_signer", job.id)),
    };
    let outputs: Vec<Output> = job
        .outputs
        .iter()
        .map(|o| {
            let raw = w.accounts.get(&o.account).map(|a| a.address(o.address));
            let raw = raw.ok_or_else(|| format!("job {}: output account {} was not derived", job.id, o.account))?;
            let recipient = Option::from(orchard::Address::from_raw_address_bytes(&raw)).ok_or("invalid output address")?;
            Ok(Output { recipient, value: o.value })
        })
        .collect::<Result<_, String>>()?;
    let built = build_bundle(rng, pk, chain_id, binding.as_deref(), tree, &spends, &outputs).map_err(|e| format!("job {}: {e}", job.id))?;
    Ok(built_out(&job.id, &built, Vec::new()))
}

/// Runs one request. `progress` is told after each finished job.
pub fn run(req: Request, mut progress: impl FnMut(usize, usize, &str)) -> Result<Response, String> {
    let mut wallet = Wallet::new();
    let mut rng = ChaCha20Rng::from_entropy();
    let pk = proving_key();
    match req {
        Request::Shields { chain_id, jobs } => {
            let mut built = Vec::with_capacity(jobs.len());
            for (i, job) in jobs.iter().enumerate() {
                built.push(shield(&mut wallet, &mut rng, &pk, &chain_id, job)?);
                progress(i + 1, jobs.len(), &job.id);
            }
            Ok(Response { built, anchor: String::new() })
        }
        Request::Spends { chain_id, leaves, expect_root, jobs } => {
            let (tree, positions) = note_tree(&leaves, &expect_root)?;
            let mut built = Vec::with_capacity(jobs.len());
            for (i, job) in jobs.iter().enumerate() {
                for o in &job.outputs {
                    wallet.account(o.account)?;
                }
                built.push(spend_job(&mut wallet, &mut rng, &pk, &chain_id, &tree, &positions, job)?);
                progress(i + 1, jobs.len(), &job.id);
            }
            Ok(Response { built, anchor: hex(&tree.root().to_bytes()) })
        }
    }
}
