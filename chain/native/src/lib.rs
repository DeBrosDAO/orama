//! Re-exports the C ABIs of both libraries so one staticlib carries them and one copy of std.
mod probestack;

pub use orama_orchard::*;
pub use wasmvm::*;
