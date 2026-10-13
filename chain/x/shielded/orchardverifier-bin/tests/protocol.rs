use std::fs;
use std::io::{Read, Write};
use std::path::Path;
use std::process::{Child, ChildStdin, ChildStdout, Command, Stdio};

const READY_ID: u64 = u64::MAX;
const OK: u8 = 0;
const MALFORMED: u8 = 1;
const BAD_PROOF_LENGTH: u8 = 2;
const PROOF_REJECTED: u8 = 3;
const SIGNATURE_REJECTED: u8 = 4;
const ACTION_LEN: usize = 820;
const HEADER_LEN: usize = 41;

struct Verifier {
    child: Child,
    stdin: ChildStdin,
    stdout: ChildStdout,
}

fn frame(id: u64, sighash: &[u8], bundle: &[u8]) -> Vec<u8> {
    let len = 8 + sighash.len() + bundle.len();
    let mut out = Vec::new();
    out.extend_from_slice(&(len as u32).to_be_bytes());
    out.extend_from_slice(&id.to_be_bytes());
    out.extend_from_slice(sighash);
    out.extend_from_slice(bundle);
    out
}

impl Verifier {
    fn start() -> Self {
        let mut child = Command::new(env!("CARGO_BIN_EXE_orama-orchard-verifier"))
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .spawn()
            .unwrap();
        let stdin = child.stdin.take().unwrap();
        let stdout = child.stdout.take().unwrap();
        let mut v = Verifier { child, stdin, stdout };
        let (id, code) = v.read_response();
        assert_eq!((id, code), (READY_ID, OK), "ready frame");
        v
    }

    fn read_response(&mut self) -> (u64, u8) {
        let mut len = [0u8; 4];
        self.stdout.read_exact(&mut len).unwrap();
        assert_eq!(u32::from_be_bytes(len), 9);
        let mut body = [0u8; 9];
        self.stdout.read_exact(&mut body).unwrap();
        (u64::from_be_bytes(body[..8].try_into().unwrap()), body[8])
    }

    fn verify(&mut self, id: u64, sighash: &[u8], bundle: &[u8]) -> (u64, u8) {
        self.stdin.write_all(&frame(id, sighash, bundle)).unwrap();
        self.stdin.flush().unwrap();
        self.read_response()
    }
}

impl Drop for Verifier {
    fn drop(&mut self) {
        let _ = self.child.kill();
        let _ = self.child.wait();
    }
}

fn load(name: &str) -> (Vec<u8>, Vec<u8>) {
    let dir = Path::new(env!("CARGO_MANIFEST_DIR")).join("../orchardffi/testdata");
    (
        fs::read(dir.join(format!("{name}.bundle"))).unwrap(),
        fs::read(dir.join(format!("{name}.sighash"))).unwrap(),
    )
}

const VECTORS: [&str; 5] = [
    "ironwood-1-action",
    "ironwood-2-action",
    "ironwood-transfer",
    "ironwood-unshield",
    "ironwood-unshield-bond",
];

#[test]
fn vectors_verify_and_ids_echo() {
    let mut v = Verifier::start();
    for (i, name) in VECTORS.iter().enumerate() {
        let (bundle, hash) = load(name);
        assert_eq!(v.verify(100 + i as u64, &hash, &bundle), (100 + i as u64, OK), "{name}");
    }
}

#[test]
fn tampering_is_rejected_with_the_same_codes_as_the_in_process_verifier() {
    let mut v = Verifier::start();
    for name in VECTORS {
        let (bundle, hash) = load(name);
        let n = bundle[0] as usize;
        let proof_at = 1 + n * ACTION_LEN + HEADER_LEN + 3;
        let flip = |at: usize| {
            let mut b = bundle.clone();
            b[at] ^= 1;
            b
        };
        assert_eq!(v.verify(1, &hash, &flip(proof_at + 200)).1, PROOF_REJECTED, "{name} proof");
        assert_eq!(v.verify(3, &hash, &flip(bundle.len() - 10)).1, SIGNATURE_REJECTED, "{name} binding");
        let mut short = bundle.clone();
        short[1 + n * ACTION_LEN + HEADER_LEN + 1] -= 1;
        assert_eq!(v.verify(4, &hash, &short).1, BAD_PROOF_LENGTH, "{name} proof length");
        assert_eq!(v.verify(5, &hash, &bundle[..bundle.len() - 1]).1, MALFORMED, "{name} truncated");
        let mut trailing = bundle.clone();
        trailing.push(0);
        assert_eq!(v.verify(6, &hash, &trailing).1, MALFORMED, "{name} trailing byte");
        let mut wrong_hash = hash.clone();
        wrong_hash[0] ^= 1;
        assert_eq!(v.verify(7, &wrong_hash, &bundle).1, SIGNATURE_REJECTED, "{name} sighash");
    }
}

#[test]
fn a_request_without_a_bundle_is_malformed_and_the_process_keeps_running() {
    let mut v = Verifier::start();
    let (bundle, hash) = load("ironwood-1-action");
    assert_eq!(v.verify(9, &hash, &[]), (9, MALFORMED));
    assert_eq!(v.verify(10, &hash, &bundle), (10, OK));
}

#[test]
fn an_oversized_frame_ends_the_process() {
    let mut v = Verifier::start();
    v.stdin.write_all(&u32::MAX.to_be_bytes()).unwrap();
    v.stdin.flush().unwrap();
    let status = v.child.wait().unwrap();
    assert!(!status.success());
}

#[test]
fn closing_stdin_ends_the_process_cleanly() {
    let mut child = Command::new(env!("CARGO_BIN_EXE_orama-orchard-verifier"))
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .spawn()
        .unwrap();
    let mut stdout = child.stdout.take().unwrap();
    let mut ready = [0u8; 13];
    stdout.read_exact(&mut ready).unwrap();
    drop(child.stdin.take());
    assert!(child.wait().unwrap().success());
}
