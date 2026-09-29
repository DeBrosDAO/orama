//! Builds real Ironwood v6 bundles: spends with Merkle witnesses, outputs, proof, signatures. The
//! bundle is authorized over the Orama sighash, and encoded as the chain's verifier reads it.
//! Proving runs here, in the wallet, and never on a node.

use orchard::builder::{Builder, BuildError, BundleType, OutputError, SpendError};
use orchard::bundle::{Authorized, BundleVersion};
use orchard::circuit::{OrchardCircuitVersion, ProvingKey};
use orchard::keys::{IncomingViewingKey, Scope, SpendAuthorizingKey};
use orchard::note::Note;
use orchard::tree::Anchor;
use orchard::value::NoteValue;
use orchard::{Address, Bundle};
use rand::{CryptoRng, RngCore};
use zcash_primitives::transaction::components::orchard::write_v6_bundle;
use zcash_protocol::value::ZatBalance;

use crate::keys::Account;
use crate::sighash::{effecting_data, sighash};
use crate::tree::NoteTree;

/// Length of the Orchard memo field.
const MEMO_LEN: usize = 512;

#[derive(Debug)]
pub enum BundleError {
    Build(BuildError),
    Spend(SpendError),
    Output(OutputError),
    /// The spent note's position is not in the tree.
    UnknownPosition(u32),
    /// The bundle has no actions (nothing to spend or pay).
    Empty,
    /// The bundle failed to encode.
    Encode(String),
    /// The value balance does not fit the wire type.
    Balance(i64),
    /// A proof could not be created.
    Prove(String),
}

impl std::fmt::Display for BundleError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{self:?}")
    }
}

impl std::error::Error for BundleError {}

/// A note to spend and where it sits in the tree.
pub struct Spend<'a> {
    pub account: &'a Account,
    pub note: Note,
    pub position: u32,
}

/// A note to create.
pub struct Output {
    pub recipient: Address,
    pub value: u64,
}

/// A finished bundle and everything the chain and the tests need to check it.
pub struct Built {
    /// Canonical v6 bundle bytes.
    pub bytes: Vec<u8>,
    /// The prefix the signatures cover.
    pub effecting_data: Vec<u8>,
    pub sighash: [u8; 32],
    pub anchor: [u8; 32],
    /// Public value balance: negative shields, positive unshields, zero is fully shielded.
    pub value_balance: i64,
    pub nullifiers: Vec<[u8; 32]>,
    pub cmxs: Vec<[u8; 32]>,
    pub bundle: Bundle<Authorized, ZatBalance>,
}

/// The proving key for the only circuit the chain verifies.
pub fn proving_key() -> ProvingKey {
    ProvingKey::build(OrchardCircuitVersion::PostNu6_3)
}

fn zat(v: i64) -> Result<ZatBalance, BundleError> {
    ZatBalance::from_i64(v).map_err(|_| BundleError::Balance(v))
}

/// Builds, proves and signs one bundle against the tree's current root.
///
/// The bundle's public value balance is what the spends hold minus what the outputs pay: nothing
/// spent and something paid shields (negative balance); more spent than paid leaves value for the
/// chain. A **transfer** must leave its fee that way (the chain refuses a transfer whose balance is
/// below `base_fee x action_gas x actions + nullifier_fee x actions`), and an **unshield** must pass
/// the binding of its signer and target ([`crate::sighash::unshield_binding`]). A shield needs the
/// nullifier fee on top of its amount in the funds that pay for it; the bundle does not carry it.
///
/// Spends need an `account` that owns the note. Outputs are sealed to their recipients, and to the
/// first spender's outgoing viewing key when there is one (a shield has no sender to recover it).
pub fn build_bundle<R: RngCore + CryptoRng>(
    rng: &mut R,
    pk: &ProvingKey,
    chain_id: &str,
    binding: Option<&[u8]>,
    tree: &NoteTree,
    spends: &[Spend<'_>],
    outputs: &[Output],
) -> Result<Built, BundleError> {
    let version = BundleVersion::ironwood_v3();
    let anchor: Anchor = tree.root();
    let mut builder = Builder::new(BundleType::DEFAULT, version, version.default_flags(), anchor)
        .map_err(BundleError::Build)?;

    let mut asks = Vec::new();
    for s in spends {
        let path = tree.witness(s.position).ok_or(BundleError::UnknownPosition(s.position))?;
        builder
            .add_spend(s.account.fvk.clone(), s.note, path)
            .map_err(BundleError::Spend)?;
        asks.push(SpendAuthorizingKey::from(&s.account.sk));
    }
    let ovk = spends.first().map(|s| s.account.fvk.to_ovk(Scope::External));
    for o in outputs {
        builder
            .add_output(ovk.clone(), o.recipient, NoteValue::from_raw(o.value), [0u8; MEMO_LEN])
            .map_err(BundleError::Output)?;
    }

    let (unauthorized, _) = builder
        .build::<i64>(&mut *rng)
        .map_err(BundleError::Build)?
        .ok_or(BundleError::Empty)?;
    let proven = unauthorized
        .create_proof(pk, &mut *rng)
        .map_err(|e| BundleError::Prove(format!("{e:?}")))?;

    // The signed prefix does not depend on the signatures, so a throwaway pass yields it.
    let probe = proven
        .clone()
        .apply_signatures(&mut *rng, [0u8; 32], &asks)
        .map_err(BundleError::Build)?;
    let hash = sighash(chain_id, binding, &encode(probe)?.0);

    let signed = proven.apply_signatures(&mut *rng, hash, &asks).map_err(BundleError::Build)?;
    let (bytes, bundle) = encode(signed)?;
    debug_assert_eq!(sighash(chain_id, binding, &bytes), hash, "prefix must not depend on signatures");

    Ok(Built {
        effecting_data: effecting_data(&bytes).to_vec(),
        sighash: hash,
        anchor: anchor.to_bytes(),
        value_balance: i64::from(*bundle.value_balance()),
        nullifiers: bundle.actions().iter().map(|a| a.nullifier().to_bytes()).collect(),
        cmxs: bundle.actions().iter().map(|a| a.cmx().to_bytes()).collect(),
        bytes,
        bundle,
    })
}

fn encode(
    bundle: Bundle<Authorized, i64>,
) -> Result<(Vec<u8>, Bundle<Authorized, ZatBalance>), BundleError> {
    let bundle = bundle.try_map_value_balance(zat)?;
    let mut bytes = Vec::new();
    write_v6_bundle(Some(&bundle), &mut bytes).map_err(|e| BundleError::Encode(e.to_string()))?;
    Ok((bytes, bundle))
}

/// Trial-decrypts every action of a bundle with an incoming viewing key: the wallet's scan step.
/// Returns `(action index, note)` for each output this key can read.
pub fn scan(bundle: &Bundle<Authorized, ZatBalance>, ivk: &IncomingViewingKey) -> Vec<(usize, Note)> {
    bundle
        .decrypt_outputs_with_keys(std::slice::from_ref(ivk))
        .into_iter()
        .map(|(idx, _, note, _, _)| (idx, note))
        .collect()
}
