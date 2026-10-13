//! Long-lived out-of-process verifier. The protocol is in `orama_orchard_verifier::protocol`; this
//! binary hands it stdin and stdout.

use std::io::{self, BufReader, BufWriter};

use orama_orchard_verifier::protocol::serve;

fn main() -> io::Result<()> {
    serve(BufReader::new(io::stdin().lock()), BufWriter::new(io::stdout().lock()))
}
