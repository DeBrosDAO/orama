//! The load generator's bundle factory: one JSON request on stdin, one JSON response on stdout.
//!
//!   cargo run --release --example gen_bench < request.json > response.json
//!
//! See `orama_shielded_wallet::bench` for the two requests (`shields` and `spends`). Progress goes to
//! stderr, one line per proved bundle.

use std::io::Read;

fn main() {
    let mut input = String::new();
    if let Err(e) = std::io::stdin().read_to_string(&mut input) {
        eprintln!("gen_bench: read the request: {e}");
        std::process::exit(2);
    }
    let req: orama_shielded_wallet::bench::Request = serde_json::from_str(&input).unwrap_or_else(|e| {
        eprintln!("gen_bench: the request is not valid: {e}");
        std::process::exit(2);
    });
    let resp = orama_shielded_wallet::bench::run(req, |done, total, id| eprintln!("gen_bench: {done}/{total} {id}")).unwrap_or_else(|e| {
        eprintln!("gen_bench: {e}");
        std::process::exit(1);
    });
    println!("{}", serde_json::to_string(&resp).expect("serializes"));
}
