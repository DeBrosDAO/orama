import { describe, expect, it } from "vitest";
import {
  LocalSigner,
  OramaChainClient,
  PUBLIC_TRANSFER_WARNING,
  PrivateTransferUnavailableError,
  addressFromPublicKey,
  transfer,
  txHashOf,
  verifyTx,
  type ShieldedTransferBuilder,
} from "../../../src/chain";
import { MSG } from "../../../src/chain";
import { TxBody, TxRaw } from "../../../src/chain/gen/cosmos/tx/v1beta1/tx";
import { SDKError } from "../../../src/errors";

const signer = new LocalSigner("22".repeat(32));
const OTHER = addressFromPublicKey(new LocalSigner("33".repeat(32)).publicKey);
const TX = { chainId: "orama-test-1", gasLimit: 90_000, feeNorama: 5_000 };
const rest = "https://node.example";
const gw = "https://gw.example";

interface Call { url: string; init?: RequestInit }

/** The hash of the transaction a request carries, as a node computes it. */
function postedHash(init?: RequestInit): string {
  return txHashOf(Uint8Array.from(Buffer.from(JSON.parse(String(init?.body)).tx_bytes, "base64")));
}

/** The messages of the transaction a call posted, after checking its signature. */
function postedMessages(call: Call) {
  const txBytes = Uint8Array.from(Buffer.from(JSON.parse(String(call.init?.body)).tx_bytes, "base64"));
  verifyTx(txBytes, "orama-test-1", 7);
  return TxBody.decode(TxRaw.decode(txBytes).bodyBytes).messages;
}

/** A chain that knows the signer's account and takes every broadcast. */
function chainFor(failOnBroadcast?: number) {
  const calls: Call[] = [];
  const fn = (async (url: string, init?: RequestInit) => {
    calls.push({ url, init });
    if (url.includes("/cosmos/auth/v1beta1/accounts/")) {
      return new Response(JSON.stringify({ account: { account_number: "7", sequence: "3" } }));
    }
    if (url.endsWith("/cosmos/tx/v1beta1/txs")) {
      return new Response(JSON.stringify({ tx_response: { code: 0, txhash: postedHash(init), raw_log: "" } }));
    }
    if (url.endsWith("/v1/chain/broadcast")) {
      if (failOnBroadcast !== undefined && calls.filter((c) => c.url.endsWith("/v1/chain/broadcast")).length - 1 === failOnBroadcast) {
        return new Response("gateway down", { status: 503 });
      }
      return new Response(JSON.stringify({ code: 0, log: "", tx_hash: postedHash(init) }));
    }
    return new Response("{}", { status: 404 });
  }) as unknown as typeof fetch;
  return { chain: new OramaChainClient({ restURL: rest, gatewayURL: gw, fetch: fn }), calls };
}

const builder = (txs: Uint8Array[]): ShieldedTransferBuilder & { requests: Array<{ to: string; amount: bigint }> } => {
  const requests: Array<{ to: string; amount: bigint }> = [];
  return {
    requests,
    async build(request) {
      requests.push(request);
      return txs;
    },
  };
};

describe("transfer: private by default", () => {
  it("throws, and touches nothing, when a private transfer has no shielded wallet", async () => {
    const { chain, calls } = chainFor();
    const err = await chain.transfer({ to: OTHER, amount: 1n }, { signer, tx: TX }).catch((e) => e);
    expect(err).toBeInstanceOf(PrivateTransferUnavailableError);
    expect(err).toBeInstanceOf(SDKError);
    expect(err.code).toBe("PRIVATE_TRANSFER_UNAVAILABLE");
    expect(err.message).toContain("public: true");
    expect(calls).toHaveLength(0);
  });

  it("treats an absent or false public as private, and signs nothing", async () => {
    for (const flag of [undefined, false]) {
      const { chain, calls } = chainFor();
      const shielded = builder([new Uint8Array([1])]);
      const result = await chain.transfer({ to: "shielded-address", amount: 5n, public: flag }, { shielded, signer, tx: TX });
      expect(result.privacy).toBe("private");
      expect(calls.every((c) => c.url.endsWith("/v1/chain/broadcast"))).toBe(true);
    }
  });

  it("refuses a public flag that is not a boolean instead of reading it as true", async () => {
    const { chain, calls } = chainFor();
    for (const flag of ["false", "true", 1, 0, {}, null]) {
      await expect(
        chain.transfer({ to: OTHER, amount: 1n, public: flag as unknown as boolean }, { signer, tx: TX }),
      ).rejects.toThrow(/boolean/);
    }
    expect(calls).toHaveLength(0);
  });

  it("builds a private transfer with the shielded wallet and broadcasts each transaction in order", async () => {
    const { chain, calls } = chainFor();
    const shielded = builder([new Uint8Array([1, 2]), new Uint8Array([3])]);
    const result = await chain.transfer({ to: "shielded-address", amount: "250" }, { shielded });
    expect(shielded.requests).toEqual([{ to: "shielded-address", amount: 250n }]);
    expect(result).toEqual({ privacy: "private", txHashes: [txHashOf(new Uint8Array([1, 2])), txHashOf(new Uint8Array([3]))] });
    expect(calls.map((c) => c.url)).toEqual([`${gw}/v1/chain/broadcast`, `${gw}/v1/chain/broadcast`]);
    expect(JSON.parse(String(calls[0]!.init?.body))).toEqual({ tx_bytes: "AQI=" });
  });

  it("names the transactions already broadcast when a later one fails", async () => {
    const { chain } = chainFor(1);
    const shielded = builder([new Uint8Array([1]), new Uint8Array([2]), new Uint8Array([3])]);
    const err = await chain.transfer({ to: "x", amount: 1n }, { shielded }).catch((e) => e);
    expect(err).toBeInstanceOf(SDKError);
    expect(err.code).toBe("PRIVATE_TRANSFER_INCOMPLETE");
    expect(err.details.txHashes).toEqual([txHashOf(new Uint8Array([1]))]);
    expect(err.message).toContain(txHashOf(new Uint8Array([1])));
  });

  it("does not wrap the failure of the first transaction, which left nothing behind", async () => {
    const { chain } = chainFor(0);
    const err = await chain.transfer({ to: "x", amount: 1n }, { shielded: builder([new Uint8Array([1])]) }).catch((e) => e);
    expect(err.code).not.toBe("PRIVATE_TRANSFER_INCOMPLETE");
  });

  it("fails when the shielded wallet builds nothing", async () => {
    const { chain } = chainFor();
    await expect(chain.transfer({ to: "x", amount: 1n }, { shielded: builder([]) })).rejects.toThrow(/no transaction/);
  });
});

describe("transfer: public by explicit choice", () => {
  it("signs a bank send and returns the warning", async () => {
    const { chain, calls } = chainFor();
    const result = await chain.transfer({ to: OTHER, amount: "1500000000", public: true }, { signer, tx: TX });
    expect(result).toMatchObject({ privacy: "public", warning: PUBLIC_TRANSFER_WARNING });
    expect((result as { txHash: string }).txHash).toMatch(/^[0-9A-F]{64}$/);
    expect(PUBLIC_TRANSFER_WARNING).toContain("visible on the chain to everyone");
    const msg = MSG.bankSend.decode(postedMessages(calls[1]!)[0]!.value);
    expect(msg.fromAddress).toBe(signer.address);
    expect(msg.toAddress).toBe(OTHER);
    expect(msg.amount).toEqual([{ denom: "norama", amount: "1500000000" }]);
  });

  it("never asks the shielded wallet", async () => {
    const { chain } = chainFor();
    const shielded = builder([new Uint8Array([1])]);
    await chain.transfer({ to: OTHER, amount: 1, public: true }, { shielded, signer, tx: TX });
    expect(shielded.requests).toHaveLength(0);
  });

  it("refuses a public transfer with no signer or no tx options", async () => {
    const { chain } = chainFor();
    await expect(chain.transfer({ to: OTHER, amount: 1n, public: true }, { tx: TX })).rejects.toThrow(/options\.signer/);
    await expect(chain.transfer({ to: OTHER, amount: 1n, public: true }, { signer })).rejects.toThrow(/options\.tx/);
  });

  it("refuses a recipient that is not an account, or is the sender", async () => {
    const { chain, calls } = chainFor();
    await expect(chain.transfer({ to: "bob", amount: 1n, public: true }, { signer, tx: TX })).rejects.toThrow(/not an orama1/);
    await expect(chain.transfer({ to: signer.address, amount: 1n, public: true }, { signer, tx: TX })).rejects.toThrow(/yourself/);
    expect(calls).toHaveLength(0);
  });
});

describe("transfer: amounts", () => {
  it("refuses an amount that is not a positive whole number of norama", async () => {
    const { chain, calls } = chainFor();
    const bad: Array<bigint | number | string> = [0n, 0, -1, 1.5, "0", "-3", "1.5", "1e9", "", "abc", Number.MAX_SAFE_INTEGER + 1, "9".repeat(39)];
    for (const amount of bad) {
      await expect(transfer(chain, { to: OTHER, amount, public: true }, { signer, tx: TX })).rejects.toThrow(RangeError);
    }
    expect(calls).toHaveLength(0);
  });

  it("takes the largest amount the guard allows", async () => {
    const { chain } = chainFor();
    await expect(chain.transfer({ to: OTHER, amount: "9".repeat(38), public: true }, { signer, tx: TX })).resolves.toMatchObject({ privacy: "public" });
  });
});

describe("withdrawEarnings", () => {
  it("builds MsgWithdrawEarnings for the signer with no destination", async () => {
    const { chain, calls } = chainFor();
    const result = await chain.withdrawEarnings(25_000_000_000n, signer, TX);
    expect(result.txHash).toMatch(/^[0-9A-F]{64}$/);
    expect(result.withdrawn).toBe("25 ORAMA (25000000000 norama)");
    const [message] = postedMessages(calls[1]!);
    expect(message!.typeUrl).toBe("/orama.fees.v1.MsgWithdrawEarnings");
    expect(MSG.feesWithdrawEarnings.decode(message!.value)).toEqual({ signer: signer.address, amount: "25000000000" });
  });

  it("refuses a zero or fractional amount before it asks the chain", async () => {
    const { chain, calls } = chainFor();
    for (const amount of [0, "0", "2.5", -1]) {
      await expect(chain.withdrawEarnings(amount, signer, TX)).rejects.toThrow(RangeError);
    }
    expect(calls).toHaveLength(0);
  });
});
