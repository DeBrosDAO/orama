import { describe, expect, it } from "vitest";
import {
  LocalSigner,
  MSG,
  addressFromPublicKey,
  addressToBytes,
  assembleTx,
  buildSignDoc,
  isOramaAddress,
  signTx,
  toHex,
  verifyDirectSignature,
  verifyTx,
  type OramaSigner,
} from "../../../src/chain";

// The BIP-39 "abandon ... about" leaf at coin type 118; the same vector as
// core/pkg/rwagent/orama_tx_test.go and chain/client/tx.
const KEY = "c4a48e2fce1481cd3294b4490f6678090ea98d3d0e5cd984558ab0968741b104";
const ADDRESS = "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s";
const OTHER = "orama1gpq5ys6yg4rywjzfff95cn2wfag9z5jn2mvlr0";

const send = () => MSG.bankSend.create({ fromAddress: ADDRESS, toAddress: OTHER, amount: [{ denom: "norama", amount: "1" }] });
const tx = () => ({ chainId: "orama-test-1", accountNumber: 9, sequence: 2, gasLimit: 100000, feeNorama: 5000, msgs: [send()] });

describe("addresses", () => {
  it("derives the RootWallet vector's address", () => {
    expect(new LocalSigner(KEY).address).toBe(ADDRESS);
  });

  it("round-trips an address through its bytes", () => {
    expect(addressToBytes(ADDRESS)).toHaveLength(20);
    expect(addressFromPublicKey(new LocalSigner(KEY).publicKey)).toBe(ADDRESS);
  });

  it("refuses a bad checksum, a foreign prefix and a wrong length", () => {
    expect(isOramaAddress(ADDRESS)).toBe(true);
    expect(isOramaAddress(ADDRESS.slice(0, -1) + "q")).toBe(false);
    expect(isOramaAddress("cosmos19rl4cm2hmr8afy4kldpxz3fka4jguq0a3fzfax")).toBe(false);
    expect(isOramaAddress("orama1")).toBe(false);
    expect(isOramaAddress("")).toBe(false);
  });

  it("refuses a public key that is not compressed", () => {
    expect(() => addressFromPublicKey(new Uint8Array(65))).toThrow(/33 bytes/);
  });
});

describe("LocalSigner", () => {
  it("refuses keys that are not secp256k1 scalars", () => {
    expect(() => new LocalSigner(new Uint8Array(32))).toThrow(/valid secp256k1/);
    expect(() => new LocalSigner(new Uint8Array(31).fill(1))).toThrow(/valid secp256k1/);
    expect(() => new LocalSigner("ff".repeat(32))).toThrow(/valid secp256k1/);
    expect(() => new LocalSigner("zz")).toThrow();
  });

  it("signs deterministically with a low-s signature that verifies", async () => {
    const signer = new LocalSigner(KEY);
    const doc = new Uint8Array([1, 2, 3]);
    const a = await signer.signDirect(doc);
    const b = await signer.signDirect(doc);
    expect(toHex(a)).toBe(toHex(b));
    expect(a).toHaveLength(64);
    expect(verifyDirectSignature(signer.publicKey, doc, a)).toBe(true);
    expect(verifyDirectSignature(signer.publicKey, new Uint8Array([1, 2, 4]), a)).toBe(false);
    expect(verifyDirectSignature(signer.publicKey, doc, a.slice(0, 63))).toBe(false);
  });
});

describe("building a transaction", () => {
  it("signs, verifies and assembles", async () => {
    const signer = new LocalSigner(KEY);
    const signed = await signTx(tx(), signer);
    const pub = verifyTx(signed.txBytes, "orama-test-1", 9);
    expect(toHex(pub)).toBe(toHex(signer.publicKey));
  });

  it("changes the sign bytes with the chain id, the account number and the sequence", () => {
    const pub = new LocalSigner(KEY).publicKey;
    const base = toHex(buildSignDoc(tx(), pub).signDoc);
    expect(toHex(buildSignDoc({ ...tx(), chainId: "other" }, pub).signDoc)).not.toBe(base);
    expect(toHex(buildSignDoc({ ...tx(), accountNumber: 10 }, pub).signDoc)).not.toBe(base);
    expect(toHex(buildSignDoc({ ...tx(), sequence: 3 }, pub).signDoc)).not.toBe(base);
  });

  it("refuses an empty chain id, no messages, and a fee that is not positive", () => {
    const pub = new LocalSigner(KEY).publicKey;
    expect(() => buildSignDoc({ ...tx(), chainId: "" }, pub)).toThrow(/chain id/);
    expect(() => buildSignDoc({ ...tx(), msgs: [] }, pub)).toThrow(/no messages/);
    expect(() => buildSignDoc({ ...tx(), feeNorama: 0 }, pub)).toThrow(/positive/);
    expect(() => buildSignDoc({ ...tx(), feeNorama: -5 }, pub)).toThrow(/positive/);
  });

  it("refuses a signature that is not 64 bytes", () => {
    const doc = buildSignDoc(tx(), new LocalSigner(KEY).publicKey);
    expect(() => assembleTx(doc, new Uint8Array(63))).toThrow(/64/);
  });

  it("refuses a signer that signs the wrong document", async () => {
    const real = new LocalSigner(KEY);
    const liar: OramaSigner = {
      address: real.address,
      publicKey: real.publicKey,
      signDirect: () => real.signDirect(new Uint8Array([9, 9, 9])),
    };
    await expect(signTx(tx(), liar)).rejects.toThrow(/does not verify/);
  });

  it("refuses a signer that answers with the wrong length", async () => {
    const real = new LocalSigner(KEY);
    const short: OramaSigner = { address: real.address, publicKey: real.publicKey, signDirect: async () => new Uint8Array(10) };
    await expect(signTx(tx(), short)).rejects.toThrow(/does not verify/);
  });

  it("hands the signer the SignDoc, so it can decode what it approves", async () => {
    const real = new LocalSigner(KEY);
    let seen: Uint8Array | undefined;
    const spy: OramaSigner = {
      address: real.address,
      publicKey: real.publicKey,
      signDirect: (doc) => {
        seen = doc;
        return real.signDirect(doc);
      },
    };
    const signed = await signTx(tx(), spy);
    expect(toHex(seen!)).toBe(toHex(signed.signDoc));
  });
});
