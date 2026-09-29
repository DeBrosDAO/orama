import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import {
  LocalSigner,
  MESSAGE_REGISTRY,
  buildSignDoc,
  describeTx,
  fromHex,
  signTx,
  toHex,
  verifyTx,
  type AnyMsg,
} from "../../../src/chain";

/**
 * The cross-language contract. chain/client/tx (Go) builds each of these
 * transactions and writes the bytes into testdata; the TypeScript builder must
 * produce the identical body, auth info, sign doc, signature and transaction.
 * If the Go builder's bytes change, its own test fails until the fixture is
 * regenerated, and this one fails until the TypeScript side agrees.
 */
const testdata = resolve(__dirname, "../../../../chain/client/tx/testdata");

interface VectorCase {
  name: string;
  type_url: string;
  msg: unknown;
  memo?: string;
  body_bytes_hex: string;
  auth_info_bytes_hex: string;
  sign_doc_hex: string;
  signature_hex: string;
  tx_hex: string;
}

const vectors = JSON.parse(readFileSync(resolve(testdata, "tx_vectors.json"), "utf8")) as {
  chain_id: string;
  private_key_hex: string;
  address: string;
  pubkey_hex: string;
  account_number: number;
  sequence: number;
  gas_limit: number;
  fee_norama: string;
  cases: VectorCase[];
};

const walletMsgs = JSON.parse(readFileSync(resolve(testdata, "wallet_msgs.json"), "utf8")) as {
  orama_msgs: string[];
  sdk_msgs: string[];
};

/**
 * CosmWasm's `msg` field is protobuf bytes, but Go's JSON form of it is the
 * embedded JSON object itself (RawContractMessage). ts-proto reads bytes as
 * base64, so the fixture's object is converted back to the bytes it stands for.
 */
function protoJSON(c: VectorCase): unknown {
  const msg = c.msg as Record<string, unknown>;
  if (c.type_url.startsWith("/cosmwasm.") && typeof msg.msg === "object" && msg.msg !== null) {
    return { ...msg, msg: Buffer.from(JSON.stringify(msg.msg)).toString("base64") };
  }
  return c.msg;
}

function anyMsg(c: VectorCase): AnyMsg {
  const def = MESSAGE_REGISTRY.get(c.type_url);
  if (!def) throw new Error(`no codec for ${c.type_url}`);
  return { typeUrl: c.type_url, value: def.codec.encode(def.codec.fromJSON(protoJSON(c))).finish() };
}

describe("cross-language vectors (Go chain/client/tx)", () => {
  const signer = new LocalSigner(vectors.private_key_hex);

  it("derives the fixture's address and public key from its key", () => {
    expect(signer.address).toBe(vectors.address);
    expect(toHex(signer.publicKey)).toBe(vectors.pubkey_hex);
  });

  it("covers a spread of messages", () => {
    expect(vectors.cases.length).toBeGreaterThanOrEqual(15);
  });

  for (const c of vectors.cases) {
    describe(c.name, () => {
      const unsigned = {
        chainId: vectors.chain_id,
        accountNumber: vectors.account_number,
        sequence: vectors.sequence,
        gasLimit: vectors.gas_limit,
        feeNorama: vectors.fee_norama,
        msgs: [anyMsg(c)],
        memo: c.memo,
      };

      it("builds the same body, auth info and sign bytes", () => {
        const doc = buildSignDoc(unsigned, signer.publicKey);
        expect(toHex(doc.bodyBytes)).toBe(c.body_bytes_hex);
        expect(toHex(doc.authInfoBytes)).toBe(c.auth_info_bytes_hex);
        expect(toHex(doc.signDoc)).toBe(c.sign_doc_hex);
      });

      it("produces the same signature and transaction bytes", async () => {
        const signed = await signTx(unsigned, signer);
        expect(toHex(signed.signature)).toBe(c.signature_hex);
        expect(toHex(signed.txBytes)).toBe(c.tx_hex);
      });

      it("verifies the Go-built transaction", () => {
        const pub = verifyTx(fromHex(c.tx_hex), vectors.chain_id, vectors.account_number);
        expect(toHex(pub)).toBe(vectors.pubkey_hex);
      });

      it("describes the Go-built transaction without an unknown message", () => {
        const d = describeTx(fromHex(c.tx_hex));
        expect(d.signer).toBe(vectors.address);
        expect(d.messages).toHaveLength(1);
        expect(d.messages[0].typeUrl).toBe(c.type_url);
        expect(d.messages[0].title).not.toBe("Unknown message");
      });
    });
  }

  it("rejects a Go-built transaction under another chain id or account number", () => {
    const tx = fromHex(vectors.cases[0].tx_hex);
    expect(() => verifyTx(tx, "other-chain", vectors.account_number)).toThrow(/does not verify/);
    expect(() => verifyTx(tx, vectors.chain_id, vectors.account_number + 1)).toThrow(/does not verify/);
  });
});

describe("the Go side's message list", () => {
  it("has a decoder for every orama message the chain registers", () => {
    expect(walletMsgs.orama_msgs.length).toBeGreaterThanOrEqual(50);
    const missing = walletMsgs.orama_msgs.filter((url) => !MESSAGE_REGISTRY.has(url));
    expect(missing).toEqual([]);
  });

  it("has a decoder for every SDK message a wallet signs", () => {
    const missing = walletMsgs.sdk_msgs.filter((url) => !MESSAGE_REGISTRY.has(url));
    expect(missing).toEqual([]);
  });

  it("holds no message the Go side does not list", () => {
    const listed = new Set([...walletMsgs.orama_msgs, ...walletMsgs.sdk_msgs]);
    const extra = [...MESSAGE_REGISTRY.keys()].filter((url) => !listed.has(url));
    expect(extra).toEqual([]);
  });
});
