//! Regenerates testdata/bundles/scenario.json. Deterministic: the same code writes the same bytes.
//!
//!   cargo run --release --example gen_scenario > testdata/bundles/scenario.json

fn main() {
    let scenario = orama_shielded_wallet::scenario::run().expect("scenario builds");
    println!("{}", serde_json::to_string_pretty(&scenario).expect("serializes"));
}
