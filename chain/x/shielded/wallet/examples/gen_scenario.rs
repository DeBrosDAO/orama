//! Regenerates testdata/bundles/scenario.json. Deterministic: the same code writes the same bytes.
//!
//!   cargo run --release --example gen_scenario > testdata/bundles/scenario.json
//!
//! For a live chain, set ORAMA_SCENARIO_CHAIN_ID, ORAMA_SCENARIO_UNSHIELD_SIGNER (the 20 address
//! bytes of the account that signs the unshield, in hex), ORAMA_SCENARIO_SCALE and ORAMA_SCENARIO_FEE;
//! with none set the output is the committed scenario.

fn main() {
    let cfg = orama_shielded_wallet::scenario::Config::from_env(|k| std::env::var(k).ok()).unwrap_or_else(|e| {
        eprintln!("gen_scenario: {e}");
        std::process::exit(2);
    });
    let scenario = orama_shielded_wallet::scenario::run_with(&cfg).expect("scenario builds");
    println!("{}", serde_json::to_string_pretty(&scenario).expect("serializes"));
}
