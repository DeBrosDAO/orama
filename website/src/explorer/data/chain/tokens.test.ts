import { describe, expect, it } from "vitest";
import { ChainReadError, createClient } from "./client";
import { FakeGateway, addr } from "./fake-gateway";
import { queryPath } from "./module-query";
import { loadFrozen, loadHolders, loadToken, loadTokenParams, loadTokens } from "./tokens";

const CREATOR = addr(3);
const DENOM = `factory/${CREATOR}/cash`;
const TOTAL_SUPPLY = "cosmos.bank.v1beta1.Query/TotalSupply";

const client = (gw: FakeGateway) => createClient(gw.fetch, "https://gw.example/v1/chain");
const firstPage = { limit: 20, key: null };

function tokenRecord(over: Record<string, unknown> = {}) {
  return {
    denom: DENOM,
    creator: CREATOR,
    name: "Cash",
    symbol: "CASH",
    description: "A test token",
    extensions: { mint: true, freeze: false, permanent_delegate: "", transfer_fee_bps: 250, non_transferable: false, pause: false, transfer_hook: "" },
    paused: false,
    shieldable: true,
    issued: "1000000",
    deposit_amount: "70000000",
    ...over,
  };
}

describe("loadTokens", () => {
  it("TestLoadTokens_lists_factory_denoms_and_leaves_the_native_coin_out", async () => {
    const gw = new FakeGateway().on(queryPath(TOTAL_SUPPLY, { pagination: { limit: 20 } }), {
      supply: [
        { denom: DENOM, amount: "1000000" },
        { denom: "norama", amount: "41000000000000000" },
      ],
      pagination: { next_key: "", total: "0" },
    });
    const page = await loadTokens(client(gw), firstPage);
    expect(page.items).toEqual([{ denom: DENOM, creator: CREATOR, subdenom: "cash", supply: "1000000" }]);
    expect(page.nextKey).toBeNull();
  });

  it("TestLoadTokens_stops_paging_once_the_bank_is_past_the_factory_run", async () => {
    const gw = new FakeGateway().on(queryPath(TOTAL_SUPPLY, { pagination: { limit: 20 } }), {
      supply: [
        { denom: DENOM, amount: "5" },
        { denom: "norama", amount: "7" },
      ],
      pagination: { next_key: "bm9yYW1h", total: "0" },
    });
    expect((await loadTokens(client(gw), firstPage)).nextKey).toBeNull();
  });

  it("TestLoadTokens_a_full_page_of_tokens_offers_the_next_key_and_sends_it_back", async () => {
    const gw = new FakeGateway()
      .on(queryPath(TOTAL_SUPPLY, { pagination: { limit: 20 } }), { supply: [{ denom: DENOM, amount: "5" }], pagination: { next_key: "a2V5MQ==" } })
      .on(queryPath(TOTAL_SUPPLY, { pagination: { limit: 20, key: "a2V5MQ==" } }), { supply: [], pagination: { next_key: null } });
    const first = await loadTokens(client(gw), firstPage);
    expect(first.nextKey).toBe("a2V5MQ==");
    const second = await loadTokens(client(gw), { limit: 20, key: first.nextKey });
    expect(second).toEqual({ items: [], nextKey: null });
  });

  it("TestLoadTokens_an_empty_chain_is_an_empty_list_not_an_error", async () => {
    const gw = new FakeGateway().on(queryPath(TOTAL_SUPPLY, { pagination: { limit: 20 } }), { supply: [{ denom: "norama", amount: "1" }], pagination: null });
    expect(await loadTokens(client(gw), firstPage)).toEqual({ items: [], nextKey: null });
  });

  it("TestLoadTokens_a_denom_with_a_bad_creator_checksum_rejects_the_read", async () => {
    const gw = new FakeGateway().on(queryPath(TOTAL_SUPPLY, { pagination: { limit: 20 } }), {
      supply: [{ denom: "factory/orama1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq/cash", amount: "1" }],
    });
    await expect(loadTokens(client(gw), firstPage)).rejects.toBeInstanceOf(ChainReadError);
  });

  it("TestLoadTokens_clamps_the_page_to_what_the_gateway_serves", async () => {
    const gw = new FakeGateway().on(queryPath(TOTAL_SUPPLY, { pagination: { limit: 100 } }), { supply: [] });
    await loadTokens(client(gw), { limit: 5000, key: null });
    expect(gw.asked).toEqual([queryPath(TOTAL_SUPPLY, { pagination: { limit: 100 } })]);
  });
});

describe("loadToken", () => {
  it("TestLoadToken_reads_the_record_and_its_capabilities", async () => {
    const gw = new FakeGateway().on(queryPath("orama.token.v1.Query/Token", { denom: DENOM }), { token: tokenRecord() });
    const token = await loadToken(client(gw), DENOM);
    expect(token).toMatchObject({ name: "Cash", symbol: "CASH", subdenom: "cash", issued: "1000000", shieldable: true });
    expect(token?.extensions).toEqual({ mint: true, freeze: false, permanentDelegate: null, transferFeeBps: 250, nonTransferable: false, pause: false, transferHook: null });
  });

  it("TestLoadToken_a_token_the_chain_does_not_have_is_null_and_asks_nothing_for_a_non_factory_denom", async () => {
    const gw = new FakeGateway();
    expect(await loadToken(client(gw), DENOM)).toBeNull();
    expect(await loadToken(client(gw), "norama")).toBeNull();
    expect(gw.asked).toEqual([queryPath("orama.token.v1.Query/Token", { denom: DENOM })]);
  });

  it("TestLoadToken_cleans_text_the_creator_wrote", async () => {
    const gw = new FakeGateway().on(queryPath("orama.token.v1.Query/Token", { denom: DENOM }), {
      token: tokenRecord({ name: "Ca‮sh", description: "line\u0000break" }),
    });
    const token = await loadToken(client(gw), DENOM);
    expect(token?.name).toBe("Cash");
    expect(token?.description).toBe("linebreak");
  });

  it("TestLoadToken_an_answer_for_another_denom_is_rejected", async () => {
    const gw = new FakeGateway().on(queryPath("orama.token.v1.Query/Token", { denom: DENOM }), { token: tokenRecord({ denom: `factory/${CREATOR}/other` }) });
    await expect(loadToken(client(gw), DENOM)).rejects.toBeInstanceOf(ChainReadError);
  });

  it("TestLoadToken_a_malformed_record_names_the_field_and_not_the_value", async () => {
    const gw = new FakeGateway().on(queryPath("orama.token.v1.Query/Token", { denom: DENOM }), { token: tokenRecord({ issued: "-5" }) });
    await expect(loadToken(client(gw), DENOM)).rejects.toThrow("The chain sent a malformed token issued supply.");
  });
});

describe("holders, frozen and parameters", () => {
  it("TestLoadHolders_lists_balances_in_the_order_the_chain_gives", async () => {
    const holder = (n: number, amount: string) => ({ address: addr(n), balance: { denom: DENOM, amount } });
    const gw = new FakeGateway().on(queryPath("cosmos.bank.v1beta1.Query/DenomOwners", { denom: DENOM, pagination: { limit: 20 } }), {
      denom_owners: [holder(1, "10"), holder(2, "20")],
      pagination: { next_key: "" },
    });
    const page = await loadHolders(client(gw), DENOM, firstPage);
    expect(page.items.map((h) => h.balance)).toEqual(["10", "20"]);
  });

  it("TestLoadFrozen_reads_the_flag", async () => {
    const gw = new FakeGateway().on(queryPath("orama.token.v1.Query/Frozen", { denom: DENOM, account: CREATOR }), { frozen: true });
    expect(await loadFrozen(client(gw), DENOM, CREATOR)).toBe(true);
  });

  it("TestLoadTokenParams_reads_the_creation_fee", async () => {
    const gw = new FakeGateway().on(queryPath("orama.token.v1.Query/Params"), { params: { creation_fee: "10000000000", deposit_per_byte: "70000" } });
    expect(await loadTokenParams(client(gw))).toEqual({ creationFee: "10000000000", depositPerByte: "70000" });
  });
});
