//! The wallet's note serialization: what a wallet stores for a note it owns so that it can be
//! spent later. One note is `address(43) || value(u64 le) || rho(32) || rseed(32) || version(1)`,
//! hex-encoded; `version` is 2 or 3 (Orchard note version). The format is fixed: stored wallets and
//! the load generator (`oramad-bench`) read it, so a change here breaks them.

use orchard::note::{Note, NoteVersion, RandomSeed, Rho};
use orchard::value::NoteValue;

use crate::vectors::hex;

/// Length of a raw Orchard address.
const ADDRESS_LEN: usize = 43;
/// Length of a serialized note.
pub const NOTE_LEN: usize = ADDRESS_LEN + 8 + 32 + 32 + 1;
const VERSION_V2: u8 = 2;
const VERSION_V3: u8 = 3;

/// Serializes a note to its hex storage form.
pub fn encode_note(n: &Note) -> String {
    let mut b = Vec::with_capacity(NOTE_LEN);
    b.extend_from_slice(&n.recipient().to_raw_address_bytes());
    b.extend_from_slice(&n.value().inner().to_le_bytes());
    b.extend_from_slice(&n.rho().to_bytes());
    b.extend_from_slice(n.rseed().as_bytes());
    b.push(match n.version() {
        NoteVersion::V2 => VERSION_V2,
        NoteVersion::V3 => VERSION_V3,
    });
    hex(&b)
}

/// Parses the hex storage form of a note, refusing anything that is not exactly one valid note.
pub fn decode_note(s: &str) -> Result<Note, String> {
    let b = hex::decode(s).map_err(|e| format!("note is not hex: {e}"))?;
    if b.len() != NOTE_LEN {
        return Err(format!("note is {} bytes, want {NOTE_LEN}", b.len()));
    }
    let addr_bytes: [u8; ADDRESS_LEN] = b[..ADDRESS_LEN].try_into().map_err(|_| "address bytes")?;
    let recipient = Option::from(orchard::Address::from_raw_address_bytes(&addr_bytes)).ok_or("note address is invalid")?;
    let value = NoteValue::from_raw(u64::from_le_bytes(b[ADDRESS_LEN..ADDRESS_LEN + 8].try_into().map_err(|_| "value bytes")?));
    let rho_bytes: [u8; 32] = b[ADDRESS_LEN + 8..ADDRESS_LEN + 40].try_into().map_err(|_| "rho bytes")?;
    let rho = Option::from(Rho::from_bytes(&rho_bytes)).ok_or("note rho is invalid")?;
    let rseed_bytes: [u8; 32] = b[ADDRESS_LEN + 40..ADDRESS_LEN + 72].try_into().map_err(|_| "rseed bytes")?;
    let rseed = Option::from(RandomSeed::from_bytes(rseed_bytes, &rho)).ok_or("note rseed does not match its rho")?;
    let version = match b[NOTE_LEN - 1] {
        VERSION_V2 => NoteVersion::V2,
        VERSION_V3 => NoteVersion::V3,
        v => return Err(format!("unknown note version {v}")),
    };
    Option::from(Note::from_parts(recipient, value, rho, rseed, version)).ok_or_else(|| "note is not a valid note".to_string())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::keys::Account;
    use crate::vectors::abandon_seed;

    const GOLDEN_ADDRESS_HEX: &str = "e3c70b2a3907f408fe802771fb07824ec6e826bf3d7c7586fe62f2e04cdbad32678fb37171a2fd1cbfb02e";
    const GOLDEN_NOTE_HEX: &str = concat!(
        "e3c70b2a3907f408fe802771fb07824ec6e826bf3d7c7586fe62f2e04cdbad32678fb37171a2fd1cbfb02e", // address
        "0903000000000000", // value 777, u64 le
        "0100000000000000000000000000000000000000000000000000000000000000", // rho
        "0202020202020202020202020202020202020202020202020202020202020202", // rseed
        "03", // version 3
    );

    /// A note built from fixed parts: account 0 / address 0 of the public test seed, value 777,
    /// rho = 1, rseed = 2s, version 3.
    fn fixed_note() -> Note {
        let acct = Account::derive(&abandon_seed(), 0).expect("derive");
        let recipient = Option::from(orchard::Address::from_raw_address_bytes(&acct.address(0))).expect("address");
        let mut rho_bytes = [0u8; 32];
        rho_bytes[0] = 1;
        let rho = Option::from(Rho::from_bytes(&rho_bytes)).expect("rho");
        let rseed = Option::from(RandomSeed::from_bytes([2u8; 32], &rho)).expect("rseed");
        Option::from(Note::from_parts(recipient, NoteValue::from_raw(777), rho, rseed, NoteVersion::V3)).expect("note")
    }

    #[test]
    fn encode_note_matches_the_golden_bytes() {
        let acct = Account::derive(&abandon_seed(), 0).expect("derive");
        assert_eq!(hex(&acct.address(0)), GOLDEN_ADDRESS_HEX);
        assert_eq!(encode_note(&fixed_note()), GOLDEN_NOTE_HEX);
    }

    #[test]
    fn layout_is_address_value_rho_rseed_version() {
        let b = hex::decode(encode_note(&fixed_note())).expect("hex");
        assert_eq!(b.len(), NOTE_LEN);
        assert_eq!(&b[..ADDRESS_LEN], hex::decode(GOLDEN_ADDRESS_HEX).unwrap().as_slice());
        assert_eq!(&b[ADDRESS_LEN..ADDRESS_LEN + 8], &777u64.to_le_bytes());
        assert_eq!(b[ADDRESS_LEN + 8], 1);
        assert_eq!(&b[ADDRESS_LEN + 40..ADDRESS_LEN + 72], &[2u8; 32]);
        assert_eq!(b[NOTE_LEN - 1], VERSION_V3);
    }

    #[test]
    fn round_trip_and_golden_decode() {
        let note = decode_note(GOLDEN_NOTE_HEX).expect("golden decodes");
        assert_eq!(note.value().inner(), 777);
        assert_eq!(encode_note(&note), GOLDEN_NOTE_HEX);
    }

    #[test]
    fn corrupt_notes_are_refused() {
        assert!(decode_note("").is_err(), "empty");
        assert!(decode_note("zz").is_err(), "not hex");
        assert!(decode_note(&GOLDEN_NOTE_HEX[..GOLDEN_NOTE_HEX.len() - 2]).is_err(), "short");
        let mut bad = hex::decode(GOLDEN_NOTE_HEX).unwrap();
        *bad.last_mut().unwrap() = 9;
        assert!(decode_note(&hex(&bad)).is_err(), "unknown version");
        let mut long = hex::decode(GOLDEN_NOTE_HEX).unwrap();
        long.push(0);
        assert!(decode_note(&hex(&long)).is_err(), "long");
    }
}
