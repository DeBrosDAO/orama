#!/usr/bin/env python3
"""Regenerates addresses.json, the shielded address vectors the Rust, Go and TypeScript codecs share.

    python3 generate.py > addresses.json

It is written against the BIP-350 reference code, independent of the three implementations it
checks: the raw addresses come from ../keys/keys.json (the key-tree vectors), the strings from the
reference Bech32m, and every invalid string is a valid one broken in exactly one way. Deterministic.
"""
import json
import pathlib

CHARSET = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"
BECH32M_CONST = 0x2BC830A3
HRP = "oramas"


def polymod(values):
    gen = [0x3B6A57B2, 0x26508E6D, 0x1EA119FA, 0x3D4233DD, 0x2A1462B3]
    chk = 1
    for v in values:
        top = chk >> 25
        chk = (chk & 0x1FFFFFF) << 5 ^ v
        for i in range(5):
            chk ^= gen[i] if ((top >> i) & 1) else 0
    return chk


def hrp_expand(hrp):
    return [ord(x) >> 5 for x in hrp] + [0] + [ord(x) & 31 for x in hrp]


def checksum(hrp, data, const):
    mod = polymod(hrp_expand(hrp) + data + [0] * 6) ^ const
    return [(mod >> 5 * (5 - i)) & 31 for i in range(6)]


def convert(data, frombits, tobits, pad=True):
    acc, bits, ret = 0, 0, []
    maxv = (1 << tobits) - 1
    for value in data:
        acc = (acc << frombits) | value
        bits += frombits
        while bits >= tobits:
            bits -= tobits
            ret.append((acc >> bits) & maxv)
    if pad and bits:
        ret.append((acc << (tobits - bits)) & maxv)
    return ret


def encode_words(hrp, words, const=BECH32M_CONST):
    return hrp + "1" + "".join(CHARSET[d] for d in words + checksum(hrp, words, const))


def encode(raw, hrp=HRP, const=BECH32M_CONST):
    return encode_words(hrp, convert(list(raw), 8, 5), const)


def flip(address, at):
    """The address with the data character at `at` (counted from the separator) changed."""
    i = address.rindex("1") + 1 + at
    other = CHARSET[(CHARSET.index(address[i]) + 1) % 32]
    return address[:i] + other + address[i + 1 :]


def main():
    keys = json.loads((pathlib.Path(__file__).parent.parent / "keys" / "keys.json").read_text())
    raws = []
    for s, a, j in [(0, 0, 0), (0, 0, 1), (0, 0, 2), (0, 1, 0), (1, 0, 0), (1, 1, 2), (2, 0, 1)]:
        raws.append(keys["seeds"][s]["accounts"][a]["addresses"][j])
    raws += ["00" * 43, "ff" * 43]
    valid = [{"raw": r, "address": encode(bytes.fromhex(r))} for r in raws]

    good = valid[0]["address"]
    raw0 = bytes.fromhex(valid[0]["raw"])
    words = convert(list(raw0), 8, 5)
    padded = words[:-1] + [words[-1] | 1]  # the last group's spare bit set

    invalid = [
        {"why": "one data character changed", "address": flip(good, 10), "error": "bad_checksum"},
        {"why": "the last checksum character changed", "address": flip(good, 74), "error": "bad_checksum"},
        {"why": "the original Bech32 checksum", "address": encode(raw0, const=1), "error": "bad_checksum"},
        {"why": "mixed case", "address": good[:20] + good[20:].upper(), "error": "mixed_case"},
        {"why": "no separator", "address": good.replace("1", "q"), "error": "no_separator"},
        {"why": "empty", "address": "", "error": "no_separator"},
        {"why": "another human-readable part, valid checksum", "address": encode(raw0, hrp="orama"), "error": "wrong_prefix"},
        {"why": "a transparent account address prefix", "address": encode(raw0[:20], hrp="orama"), "error": "wrong_prefix"},
        {"why": "no human-readable part", "address": good[len(HRP) :], "error": "wrong_prefix"},
        {"why": "a character outside the alphabet", "address": good[:30] + "b" + good[31:], "error": "bad_character"},
        {"why": "a space", "address": good[:30] + " " + good[31:], "error": "bad_character"},
        {"why": "42 bytes, valid checksum", "address": encode(raw0[:42]), "error": "wrong_length"},
        {"why": "44 bytes, valid checksum", "address": encode(raw0 + b"\x00"), "error": "wrong_length"},
        {"why": "one character short", "address": good[:-1], "error": "wrong_length"},
        {"why": "one character long", "address": good + "q", "error": "wrong_length"},
        {"why": "a spare padding bit set, valid checksum", "address": encode_words(HRP, padded), "error": "bad_padding"},
    ]

    accept = [
        {"why": "all capitals", "raw": valid[0]["raw"], "address": good.upper()},
        {"why": "all capitals, another address", "raw": valid[1]["raw"], "address": valid[1]["address"].upper()},
    ]

    out = {
        "description": "Shielded address vectors: Bech32m (BIP-350) of the 43 raw Orchard address bytes with HRP oramas. Read by the Rust, Go and TypeScript codecs. Regenerate with generate.py.",
        "hrp": HRP,
        "raw_length": 43,
        "encoded_length": 82,
        "valid": valid,
        "accept": accept,
        "invalid": invalid,
    }
    print(json.dumps(out, indent=2))


if __name__ == "__main__":
    main()
