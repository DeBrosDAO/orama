//! Long-lived out-of-process verifier. Protocol on stdin/stdout, every frame length-prefixed:
//!
//!   frame    = u32 big-endian length N, then N bytes
//!   request  = u64 BE id || sighash (32 bytes) || bundle
//!   response = u64 BE id || result code (1 byte)
//!   ready    = a response with id u64::MAX and code 0, written once after the verifying key
//!              is built. Nothing is answered before it.
//!
//! One request at a time, answered in order. A request larger than MAX_FRAME, a short read or a
//! closed stdin ends the process; the node restarts it. Nothing here logs the bundle.

use std::io::{self, BufReader, BufWriter, Read, Write};
use std::panic::{catch_unwind, AssertUnwindSafe};

use orama_orchard_verifier::{verify, verifying_key, MALFORMED, OK, PANIC};

/// Largest frame accepted: a 1 MiB bundle (the chain's limit) plus the id and the sighash.
const MAX_FRAME: usize = (1 << 20) + 8 + 32;
const READY_ID: u64 = u64::MAX;

fn read_frame(input: &mut impl Read) -> io::Result<Option<Vec<u8>>> {
    let mut len = [0u8; 4];
    match input.read_exact(&mut len) {
        Ok(()) => {}
        Err(e) if e.kind() == io::ErrorKind::UnexpectedEof => return Ok(None),
        Err(e) => return Err(e),
    }
    let len = u32::from_be_bytes(len) as usize;
    if len > MAX_FRAME {
        return Err(io::Error::new(io::ErrorKind::InvalidData, "frame too large"));
    }
    let mut body = vec![0u8; len];
    input.read_exact(&mut body)?;
    Ok(Some(body))
}

fn write_response(out: &mut impl Write, id: u64, code: u8) -> io::Result<()> {
    let mut frame = Vec::with_capacity(4 + 9);
    frame.extend_from_slice(&9u32.to_be_bytes());
    frame.extend_from_slice(&id.to_be_bytes());
    frame.push(code);
    out.write_all(&frame)?;
    out.flush()
}

/// Answers one request. A body too short to carry an id is a protocol error and ends the process.
fn answer(body: &[u8]) -> io::Result<(u64, u8)> {
    let id_bytes: [u8; 8] = body
        .get(..8)
        .and_then(|b| b.try_into().ok())
        .ok_or_else(|| io::Error::new(io::ErrorKind::InvalidData, "request has no id"))?;
    let id = u64::from_be_bytes(id_bytes);
    if body.len() < 8 + 32 + 1 {
        return Ok((id, MALFORMED));
    }
    let mut sighash = [0u8; 32];
    sighash.copy_from_slice(&body[8..40]);
    let code = catch_unwind(AssertUnwindSafe(|| verify(&body[40..], &sighash))).unwrap_or(PANIC);
    Ok((id, code))
}

fn main() -> io::Result<()> {
    let mut input = BufReader::new(io::stdin().lock());
    let mut output = BufWriter::new(io::stdout().lock());
    verifying_key();
    write_response(&mut output, READY_ID, OK)?;
    while let Some(body) = read_frame(&mut input)? {
        let (id, code) = answer(&body)?;
        write_response(&mut output, id, code)?;
    }
    Ok(())
}
