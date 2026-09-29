import { describe, expect, it } from "vitest";
import { LocalSigner, MSG, OramaChainClient, verifyTx } from "../../../src/chain";
import { NetworkError, SDKError } from "../../../src/errors";

const ADDRESS = "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s";
const HASH = "ab".repeat(32);

interface Call { url: string; init?: RequestInit }

function fakeFetch(answer: (url: string, init?: RequestInit) => { status?: number; body?: unknown; text?: string }) {
  const calls: Call[] = [];
  const fn = (async (url: string, init?: RequestInit) => {
    calls.push({ url, init });
    const a = answer(url, init);
    const text = a.text ?? JSON.stringify(a.body ?? {});
    return new Response(text, { status: a.status ?? 200 });
  }) as unknown as typeof fetch;
  return { fn, calls };
}

const gw = "https://gw.example/";

describe("gateway reads", () => {
  it("reads each route under /v1/chain/", async () => {
    const { fn, calls } = fakeFetch(() => ({ body: { ok: true } }));
    const chain = new OramaChainClient({ gatewayURL: gw, fetch: fn });
    await chain.status();
    await chain.block(12);
    await chain.blocks(1, 20);
    await chain.tx(HASH.toUpperCase());
    await chain.validators({ page: 2, perPage: 50 });
    await chain.supply();
    await chain.stakingPool();
    await chain.indexStatus();
    await chain.indexBlock(7);
    await chain.indexTx("0x" + HASH);
    await chain.indexAccountTxs(ADDRESS, { page: 1, limit: 25 });
    await chain.cnftAsset(HASH);
    await chain.cnftOwnerAssets(ADDRESS);
    expect(calls.map((c) => c.url)).toEqual([
      "https://gw.example/v1/chain/status",
      "https://gw.example/v1/chain/block?height=12",
      "https://gw.example/v1/chain/blocks?min_height=1&max_height=20",
      `https://gw.example/v1/chain/tx?hash=${HASH}`,
      "https://gw.example/v1/chain/validators?page=2&per_page=50",
      "https://gw.example/v1/chain/supply/norama",
      "https://gw.example/v1/chain/staking/pool",
      "https://gw.example/v1/chain/index/status",
      "https://gw.example/v1/chain/index/blocks/7",
      `https://gw.example/v1/chain/index/txs/${HASH}`,
      `https://gw.example/v1/chain/index/accounts/${ADDRESS}/txs?page=1&limit=25`,
      `https://gw.example/v1/chain/index/cnft/assets/${HASH}`,
      `https://gw.example/v1/chain/index/cnft/owners/${ADDRESS}/assets`,
    ]);
  });

  it("refuses malformed input before any request", async () => {
    const { fn, calls } = fakeFetch(() => ({}));
    const chain = new OramaChainClient({ gatewayURL: gw, fetch: fn });
    await expect(chain.tx("abc")).rejects.toThrow(/64 hex/);
    await expect(chain.block(-1)).rejects.toThrow(/non-negative/);
    await expect(chain.block(1.5)).rejects.toThrow(/non-negative/);
    await expect(chain.indexAccountTxs("cosmos1abc")).rejects.toThrow(/not an orama address/);
    await expect(chain.indexAccountTxs(`${ADDRESS}/../x`)).rejects.toThrow(/not an orama address/);
    await expect(chain.cnftAsset("../../etc")).rejects.toThrow(/64 hex/);
    expect(calls).toHaveLength(0);
  });

  it("names the config a read is missing", async () => {
    const chain = new OramaChainClient({});
    await expect(chain.status()).rejects.toThrow(/gatewayURL/);
    await expect(chain.account(ADDRESS)).rejects.toThrow(/restURL/);
  });

  it("raises an SDKError with the HTTP status for an error answer", async () => {
    const { fn } = fakeFetch(() => ({ status: 502, body: { error: "chain unavailable" } }));
    const chain = new OramaChainClient({ gatewayURL: gw, fetch: fn });
    await expect(chain.status()).rejects.toMatchObject({ httpStatus: 502, message: "chain unavailable" });
  });

  it("raises a NetworkError when the gateway cannot be reached", async () => {
    const fn = (async () => { throw new TypeError("connect ECONNREFUSED"); }) as unknown as typeof fetch;
    const chain = new OramaChainClient({ gatewayURL: gw, fetch: fn });
    await expect(chain.status()).rejects.toBeInstanceOf(NetworkError);
  });

  it("does not accept an HTML answer as JSON", async () => {
    const { fn } = fakeFetch(() => ({ text: "<html>" }));
    const chain = new OramaChainClient({ gatewayURL: gw, fetch: fn });
    await expect(chain.status()).rejects.toMatchObject({ code: "CHAIN_BAD_RESPONSE" });
  });
});

describe("REST reads and broadcast", () => {
  const rest = "http://node.example:31003";
  const signer = new LocalSigner("c4a48e2fce1481cd3294b4490f6678090ea98d3d0e5cd984558ab0968741b104");

  it("reads an account's number, sequence and public key", async () => {
    const key = Buffer.from(signer.publicKey).toString("base64");
    const { fn, calls } = fakeFetch(() => ({ body: { account: { account_number: "7", sequence: "3", pub_key: { key } } } }));
    const acct = await new OramaChainClient({ restURL: rest, fetch: fn }).account(ADDRESS);
    expect(calls[0].url).toBe(`${rest}/cosmos/auth/v1beta1/accounts/${ADDRESS}`);
    expect(acct.accountNumber).toBe(7n);
    expect(acct.sequence).toBe(3n);
    expect(acct.publicKey).toEqual(signer.publicKey);
  });

  it("reads balances, and an empty account has none", async () => {
    const { fn } = fakeFetch(() => ({ body: {} }));
    expect(await new OramaChainClient({ restURL: rest, fetch: fn }).balances(ADDRESS)).toEqual([]);
  });

  it("signs with the account it read and broadcasts the verified bytes", async () => {
    const { fn, calls } = fakeFetch((url) =>
      url.includes("/accounts/")
        ? { body: { account: { account_number: "7", sequence: "3" } } }
        : { body: { tx_response: { code: 0, txhash: "ABC", raw_log: "" } } },
    );
    const chain = new OramaChainClient({ restURL: rest, fetch: fn });
    const msg = MSG.bankSend.create({ fromAddress: ADDRESS, toAddress: ADDRESS, amount: [{ denom: "norama", amount: "1" }] });
    const { signed, result } = await chain.signAndBroadcast([msg], signer, { chainId: "orama-test-1", gasLimit: 90000, feeNorama: 5000 });
    expect(result.txHash).toBe("ABC");
    verifyTx(signed.txBytes, "orama-test-1", 7);
    const posted = JSON.parse(String(calls[1].init?.body));
    expect(posted.mode).toBe("BROADCAST_MODE_SYNC");
    expect(Buffer.from(posted.tx_bytes, "base64")).toEqual(Buffer.from(signed.txBytes));
  });

  it("refuses a signer whose key differs from the one the chain knows", async () => {
    const other = new LocalSigner("11".repeat(32));
    const key = Buffer.from(other.publicKey).toString("base64");
    const { fn } = fakeFetch(() => ({ body: { account: { account_number: "1", sequence: "0", pub_key: { key } } } }));
    const chain = new OramaChainClient({ restURL: rest, fetch: fn });
    const msg = MSG.bankSend.create({ fromAddress: ADDRESS, toAddress: ADDRESS });
    await expect(chain.signAndBroadcast([msg], signer, { chainId: "c", gasLimit: 1, feeNorama: 1 })).rejects.toThrow(/different public key/);
  });

  it("throws the chain's rejection instead of returning it", async () => {
    const { fn } = fakeFetch(() => ({ body: { tx_response: { code: 13, txhash: "DEAD", raw_log: "insufficient fee" } } }));
    const chain = new OramaChainClient({ restURL: rest, fetch: fn });
    const err = await chain.broadcast(new Uint8Array([1])).catch((e) => e);
    expect(err).toBeInstanceOf(SDKError);
    expect(err.code).toBe("CHAIN_TX_REJECTED");
    expect(err.message).toContain("insufficient fee");
  });
});
