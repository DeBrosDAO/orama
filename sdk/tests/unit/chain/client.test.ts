import { describe, expect, it } from "vitest";
import { ChainTxRefusedError, LocalSigner, MSG, OramaChainClient, verifyTx } from "../../../src/chain";
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

describe("module queries through /v1/chain/query/", () => {
  const run = async (call: (chain: OramaChainClient) => Promise<unknown>, body: unknown = { ok: true }) => {
    const { fn, calls } = fakeFetch(() => ({ body }));
    const chain = new OramaChainClient({ gatewayURL: gw, fetch: fn });
    const result = await call(chain);
    return { result, url: new URL(calls[0]!.url), calls };
  };

  it("sends the request as JSON with the proto field names", async () => {
    const { result, url } = await run((c) => c.node("n-1"), { node: { node_id: "n-1" } });
    expect(result).toEqual({ node: { node_id: "n-1" } });
    expect(url.pathname).toBe("/v1/chain/query/orama.nodes.v1.Query/Node");
    expect(JSON.parse(url.searchParams.get("json")!)).toEqual({ node_id: "n-1" });
    expect(url.searchParams.has("height")).toBe(false);
  });

  it("maps every typed read to its service, method and request", async () => {
    const cases: Array<[(c: OramaChainClient) => Promise<unknown>, string, unknown]> = [
      [(c) => c.nodesParams(), "orama.nodes.v1.Query/Params", undefined],
      [(c) => c.operator(ADDRESS), "orama.nodes.v1.Query/Operator", { address: ADDRESS }],
      [(c) => c.nodeCluster("c-1"), "orama.nodes.v1.Query/Cluster", { cluster_id: "c-1" }],
      [(c) => c.nodeUnbondings("n-1"), "orama.nodes.v1.Query/NodeUnbondings", { node_id: "n-1" }],
      [(c) => c.nodeByName("alpha"), "orama.nodes.v1.Query/NodeByName", { name: "alpha" }],
      [(c) => c.nameOfNode("n-1"), "orama.nodes.v1.Query/NameOfNode", { node_id: "n-1" }],
      [(c) => c.nodeNames(), "orama.nodes.v1.Query/NodeNames", undefined],
      [(c) => c.nodeNames("a2V5"), "orama.nodes.v1.Query/NodeNames", { pagination: { key: "a2V5" } }],
      [(c) => c.storageParams(), "orama.storage.v1.Query/Params", undefined],
      [(c) => c.deal(7), "orama.storage.v1.Query/Deal", { deal_id: "7" }],
      [(c) => c.deal(2n ** 63n), "orama.storage.v1.Query/Deal", { deal_id: "9223372036854775808" }],
      [(c) => c.slot("7", 2), "orama.storage.v1.Query/Slot", { deal_id: "7", slot: 2 }],
      [(c) => c.storageAuthorization(ADDRESS, ADDRESS), "orama.storage.v1.Query/Authorization", { granter: ADDRESS, grantee: ADDRESS }],
      [(c) => c.storageChallenges(3, "n-1"), "orama.storage.v1.Query/Challenges", { epoch: "3", node_id: "n-1" }],
      [(c) => c.storageEpochMint(3), "orama.storage.v1.Query/EpochMint", { epoch: "3" }],
      [(c) => c.storageQueue(), "orama.storage.v1.Query/Queue", undefined],
      [(c) => c.feesParams(), "orama.fees.v1.Query/Params", undefined],
      [(c) => c.baseFee(), "orama.fees.v1.Query/BaseFee", undefined],
      [(c) => c.earnings(ADDRESS), "orama.fees.v1.Query/Earnings", { address: ADDRESS }],
      [(c) => c.feesDeposit("d-1"), "orama.fees.v1.Query/Deposit", { id: "d-1" }],
      [(c) => c.archiveParams(), "orama.archive.v1.Query/Params", undefined],
      [(c) => c.archiveRange(1, 1000), "orama.archive.v1.Query/Range", { start_height: "1", end_height: "1000" }],
      [(c) => c.lastArchivedHeight(), "orama.archive.v1.Query/LastArchivedHeight", undefined],
      [(c) => c.retainHeight(), "orama.archive.v1.Query/RetainHeight", undefined],
      [(c) => c.relayParams(), "orama.relay.v1.Query/Params", undefined],
      [(c) => c.relayReporters(), "orama.relay.v1.Query/Reporters", undefined],
      [(c) => c.relay("ab".repeat(20)), "orama.relay.v1.Query/Relay", { rsa_fingerprint_hex: "ab".repeat(20) }],
      [(c) => c.relayEpochResult(9), "orama.relay.v1.Query/EpochResult", { epoch: "9" }],
    ];
    for (const [call, name, request] of cases) {
      const { url } = await run(call);
      expect(url.pathname).toBe(`/v1/chain/query/${name}`);
      const json = url.searchParams.get("json");
      expect(json === null ? undefined : JSON.parse(json)).toEqual(request);
    }
  });

  it("passes a height and refuses a bad one before any request", async () => {
    const { url } = await run((c) => c.earnings(ADDRESS, { height: 42 }));
    expect(url.searchParams.get("height")).toBe("42");
    const { fn, calls } = fakeFetch(() => ({}));
    const chain = new OramaChainClient({ gatewayURL: gw, fetch: fn });
    await expect(chain.earnings(ADDRESS, { height: -1 })).rejects.toThrow(/non-negative/);
    await expect(chain.deal("abc")).rejects.toThrow(/unsigned 64-bit/);
    await expect(chain.deal(2n ** 64n)).rejects.toThrow(/unsigned 64-bit/);
    await expect(chain.deal(-1)).rejects.toThrow(/unsigned 64-bit/);
    await expect(chain.node("")).rejects.toThrow(/printable/);
    await expect(chain.operator("cosmos1abc")).rejects.toThrow(/orama address/);
    expect(calls).toHaveLength(0);
  });

  it("refuses anything that is not an Orama Query service", async () => {
    const { fn, calls } = fakeFetch(() => ({}));
    const chain = new OramaChainClient({ gatewayURL: gw, fetch: fn });
    await expect(chain.moduleQuery("orama.nodes.v1.Msg", "RegisterNode")).rejects.toThrow(/Query service/);
    await expect(chain.moduleQuery("cosmos.bank.v1beta1.Query", "Balance")).rejects.toThrow(/Query service/);
    await expect(chain.moduleQuery("orama.nodes.v1.Query", "Node/../x")).rejects.toThrow(/query method/);
    expect(calls).toHaveLength(0);
  });

  it("needs a gateway, and surfaces a missing key as a 404 SDKError", async () => {
    await expect(new OramaChainClient({}).baseFee()).rejects.toThrow(/gatewayURL/);
    const { fn } = fakeFetch(() => ({ status: 404, body: { error: "not found on chain" } }));
    const chain = new OramaChainClient({ gatewayURL: gw, fetch: fn });
    await expect(chain.node("nope")).rejects.toMatchObject({ httpStatus: 404 });
  });
});

describe("wallet reads and transactions through the gateway", () => {
  const VALOPER = "oramavaloper19rl4cm2hmr8afy4kldpxz3fka4jguq0a2yqwhn";
  const run = async (call: (chain: OramaChainClient) => Promise<unknown>, body: unknown = { ok: true }) => {
    const { fn, calls } = fakeFetch(() => ({ body }));
    const chain = new OramaChainClient({ gatewayURL: gw, fetch: fn });
    const result = await call(chain);
    return { result, url: new URL(calls[0]!.url), calls };
  };

  it("maps every wallet read to its service, method and request", async () => {
    const cases: Array<[(c: OramaChainClient) => Promise<unknown>, string, unknown]> = [
      [(c) => c.bankBalance(ADDRESS), "cosmos.bank.v1beta1.Query/Balance", { address: ADDRESS, denom: "norama" }],
      [(c) => c.bankAllBalances(ADDRESS, { limit: 100 }), "cosmos.bank.v1beta1.Query/AllBalances", { address: ADDRESS, pagination: { limit: 100 } }],
      [(c) => c.bankAllBalances(ADDRESS), "cosmos.bank.v1beta1.Query/AllBalances", { address: ADDRESS }],
      [(c) => c.bankSpendableBalances(ADDRESS, { key: "AQID" }), "cosmos.bank.v1beta1.Query/SpendableBalances", { address: ADDRESS, pagination: { key: "AQID" } }],
      [(c) => c.authAccount(ADDRESS), "cosmos.auth.v1beta1.Query/Account", { address: ADDRESS }],
      [(c) => c.authAccountInfo(ADDRESS), "cosmos.auth.v1beta1.Query/AccountInfo", { address: ADDRESS }],
      [(c) => c.stakingDelegation(ADDRESS, VALOPER), "cosmos.staking.v1beta1.Query/Delegation", { delegator_addr: ADDRESS, validator_addr: VALOPER }],
      [(c) => c.stakingDelegatorDelegations(ADDRESS, { limit: 5 }), "cosmos.staking.v1beta1.Query/DelegatorDelegations", { delegator_addr: ADDRESS, pagination: { limit: 5 } }],
      [(c) => c.stakingUnbondingDelegation(ADDRESS, VALOPER), "cosmos.staking.v1beta1.Query/UnbondingDelegation", { delegator_addr: ADDRESS, validator_addr: VALOPER }],
      [(c) => c.stakingDelegatorUnbondingDelegations(ADDRESS), "cosmos.staking.v1beta1.Query/DelegatorUnbondingDelegations", { delegator_addr: ADDRESS }],
      [(c) => c.stakingValidator(VALOPER), "cosmos.staking.v1beta1.Query/Validator", { validator_addr: VALOPER }],
      [(c) => c.stakingParams(), "cosmos.staking.v1beta1.Query/Params", undefined],
      [(c) => c.distributionDelegationRewards(ADDRESS, VALOPER), "cosmos.distribution.v1beta1.Query/DelegationRewards", { delegator_address: ADDRESS, validator_address: VALOPER }],
      [(c) => c.distributionDelegationTotalRewards(ADDRESS), "cosmos.distribution.v1beta1.Query/DelegationTotalRewards", { delegator_address: ADDRESS }],
      [(c) => c.contractInfo(ADDRESS), "cosmwasm.wasm.v1.Query/ContractInfo", { address: ADDRESS }],
    ];
    for (const [call, name, request] of cases) {
      const { url } = await run(call);
      expect(url.pathname).toBe(`/v1/chain/query/${name}`);
      const json = url.searchParams.get("json");
      expect(json === null ? undefined : JSON.parse(json)).toEqual(request);
    }
  });

  it("refuses bad input before any request", async () => {
    const { fn, calls } = fakeFetch(() => ({}));
    const chain = new OramaChainClient({ gatewayURL: gw, fetch: fn });
    await expect(chain.bankAllBalances(ADDRESS, { limit: 101 })).rejects.toThrow(/1 to 100/);
    await expect(chain.bankAllBalances(ADDRESS, { limit: 0 })).rejects.toThrow(/1 to 100/);
    await expect(chain.bankAllBalances(ADDRESS, { key: "not base64 !" })).rejects.toThrow(/next_key/);
    await expect(chain.bankBalance(ADDRESS, "no spaces")).rejects.toThrow(/denomination/);
    await expect(chain.stakingValidator(ADDRESS)).rejects.toThrow(/oramavaloper/);
    await expect(chain.stakingDelegation(VALOPER, VALOPER)).rejects.toThrow(/orama address/);
    await expect(chain.contractInfo("cosmos1abc")).rejects.toThrow(/orama address/);
    await expect(chain.simulateTx(new Uint8Array())).rejects.toThrow(/bytes/);
    await expect(chain.broadcastTx(new Uint8Array((1 << 20) + 1))).rejects.toThrow(/bytes/);
    expect(calls).toHaveLength(0);
  });

  it("says an address is not a contract on the typed 404, and not on any other failure", async () => {
    const notFound = fakeFetch(() => ({ status: 404, text: "not found on chain\n" }));
    expect(await new OramaChainClient({ gatewayURL: gw, fetch: notFound.fn }).isContract(ADDRESS)).toBe(false);
    const found = fakeFetch(() => ({ body: { address: ADDRESS, contract_info: {} } }));
    expect(await new OramaChainClient({ gatewayURL: gw, fetch: found.fn }).isContract(ADDRESS)).toBe(true);
    const down = fakeFetch(() => ({ status: 502, text: "chain query failed\n" }));
    await expect(new OramaChainClient({ gatewayURL: gw, fetch: down.fn }).isContract(ADDRESS)).rejects.toMatchObject({ httpStatus: 502 });
  });

  it("simulates a transaction through POST /v1/chain/simulate", async () => {
    const { fn, calls } = fakeFetch(() => ({
      body: { gas_wanted: "200000", gas_used: "123456", fee: { denom: "norama", amount: "246912" }, base_fee: "2" },
    }));
    const chain = new OramaChainClient({ gatewayURL: gw, fetch: fn });
    const result = await chain.simulateTx(new Uint8Array([1, 2, 3]));
    expect(result).toEqual({ gasWanted: 200000n, gasUsed: 123456n, fee: { denom: "norama", amount: "246912" }, baseFee: "2" });
    expect(calls[0]!.url).toBe("https://gw.example/v1/chain/simulate");
    expect(calls[0]!.init?.method).toBe("POST");
    expect(JSON.parse(String(calls[0]!.init?.body))).toEqual({ tx_bytes: "AQID" });
  });

  it("reads gas above 2^53 from its decimal string exactly, and refuses a bare number that already lost digits", async () => {
    const exact = fakeFetch(() => ({
      text: '{"gas_wanted":"18446744073709551615","gas_used":"9007199254740993","fee":{"denom":"norama","amount":"1"},"base_fee":"1"}',
    }));
    const result = await new OramaChainClient({ gatewayURL: gw, fetch: exact.fn }).simulateTx(new Uint8Array([1]));
    expect(result.gasWanted).toBe(18446744073709551615n);
    expect(result.gasUsed).toBe(9007199254740993n);

    const rounded = fakeFetch(() => ({
      text: '{"gas_wanted":18446744073709551615,"gas_used":1,"fee":{"denom":"norama","amount":"1"},"base_fee":"1"}',
    }));
    await expect(new OramaChainClient({ gatewayURL: gw, fetch: rounded.fn }).simulateTx(new Uint8Array([1]))).rejects.toMatchObject({
      code: "CHAIN_BAD_RESPONSE",
    });
  });

  it("broadcasts through POST /v1/chain/broadcast and returns the hash", async () => {
    const { fn, calls } = fakeFetch(() => ({ body: { code: 0, codespace: "", log: "", tx_hash: HASH.toUpperCase() } }));
    const chain = new OramaChainClient({ gatewayURL: gw, fetch: fn });
    expect(await chain.broadcastTx(new Uint8Array([1, 2, 3]))).toEqual({ txHash: HASH.toUpperCase(), code: 0, log: "" });
    expect(calls[0]!.url).toBe("https://gw.example/v1/chain/broadcast");
  });

  it("raises a ChainTxRefusedError with the chain's code, codespace and log", async () => {
    const { fn } = fakeFetch(() => ({ status: 422, body: { code: 32, codespace: "sdk", log: "account sequence mismatch", tx_hash: "AB" } }));
    const chain = new OramaChainClient({ gatewayURL: gw, fetch: fn });
    for (const call of [() => chain.broadcastTx(new Uint8Array([1])), () => chain.simulateTx(new Uint8Array([1]))]) {
      const err = await call().catch((e) => e);
      expect(err).toBeInstanceOf(ChainTxRefusedError);
      expect(err).toBeInstanceOf(SDKError);
      expect(err).toMatchObject({ chainCode: 32, codespace: "sdk", log: "account sequence mismatch", txHash: "AB", code: "CHAIN_TX_REJECTED", httpStatus: 422 });
    }
  });

  it("keeps other failures of the transaction routes as plain SDK errors", async () => {
    const { fn } = fakeFetch(() => ({ status: 429, text: "slow down" }));
    const chain = new OramaChainClient({ gatewayURL: gw, fetch: fn });
    const err = await chain.broadcastTx(new Uint8Array([1])).catch((e) => e);
    expect(err).not.toBeInstanceOf(ChainTxRefusedError);
    expect(err).toMatchObject({ httpStatus: 429 });
    await expect(new OramaChainClient({}).simulateTx(new Uint8Array([1]))).rejects.toThrow(/gatewayURL/);
  });
});
