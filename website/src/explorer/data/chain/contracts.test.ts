import { describe, expect, it } from "vitest";
import { createClient } from "./client";
import { loadCode, loadCodeContracts, loadCodes, loadContract, loadCreatedContracts, loadHistory, loadState } from "./contracts";
import { FakeGateway, addr } from "./fake-gateway";
import { queryPath } from "./module-query";

const client = (gw: FakeGateway) => createClient(gw.fetch, "https://gw.example/v1/chain");
const page = { limit: 20, key: null };
const first = { pagination: { limit: 20 } };
const b64 = (text: string) => btoa(text);
const checksum = btoa(String.fromCharCode(...new Uint8Array(32).fill(0xab)));

const codeInfo = (id: number, over: Record<string, unknown> = {}) => ({
  code_id: String(id),
  creator: addr(4),
  data_hash: checksum,
  instantiate_permission: { permission: "ACCESS_TYPE_EVERYBODY", addresses: [] },
  ...over,
});

describe("codes", () => {
  it("TestLoadCodes_reads_id_creator_checksum_and_who_may_instantiate", async () => {
    const gw = new FakeGateway().on(queryPath("cosmwasm.wasm.v1.Query/Codes", first), {
      code_infos: [codeInfo(1), codeInfo(2, { instantiate_permission: { permission: "ACCESS_TYPE_ANY_OF_ADDRESSES", addresses: [addr(9)] } })],
      pagination: { next_key: "" },
    });
    const result = await loadCodes(client(gw), page);
    expect(result.items.map((c) => c.codeId)).toEqual([1, 2]);
    expect(result.items[0]).toMatchObject({ creator: addr(4), checksum: "ab".repeat(32), permission: "everybody", allowed: [] });
    expect(result.items[1]).toMatchObject({ permission: "any_of", allowed: [addr(9)] });
    expect(result.nextKey).toBeNull();
  });

  it("TestLoadCodes_a_chain_with_no_code_is_an_empty_list", async () => {
    const gw = new FakeGateway().on(queryPath("cosmwasm.wasm.v1.Query/Codes", first), { code_infos: [], pagination: null });
    expect(await loadCodes(client(gw), page)).toEqual({ items: [], nextKey: null });
  });

  it("TestLoadCode_reads_the_checksum_under_its_other_name_and_a_missing_code_is_null", async () => {
    const gw = new FakeGateway().on(queryPath("cosmwasm.wasm.v1.Query/CodeInfo", { code_id: 3 }), {
      code_id: "3",
      creator: addr(4),
      checksum,
      instantiate_permission: { permission: "ACCESS_TYPE_NOBODY", addresses: [] },
    });
    expect(await loadCode(client(gw), 3)).toMatchObject({ codeId: 3, checksum: "ab".repeat(32), permission: "nobody" });
    expect(await loadCode(client(gw), 4)).toBeNull();
    expect(await loadCode(client(gw), 0)).toBeNull();
  });

  it("TestLoadCodeContracts_lists_addresses_and_pages_by_key", async () => {
    const gw = new FakeGateway().on(queryPath("cosmwasm.wasm.v1.Query/ContractsByCode", { code_id: 3, ...first }), {
      contracts: [addr(5), addr(6)],
      pagination: { next_key: "bmV4dA==" },
    });
    const result = await loadCodeContracts(client(gw), 3, page);
    expect(result).toEqual({ items: [addr(5), addr(6)], nextKey: "bmV4dA==" });
  });

  it("TestLoadCreatedContracts_reads_the_creator_listing", async () => {
    const gw = new FakeGateway().on(queryPath("cosmwasm.wasm.v1.Query/ContractsByCreator", { creator_address: addr(4), ...first }), {
      contract_addresses: [addr(5)],
    });
    expect((await loadCreatedContracts(client(gw), addr(4), page)).items).toEqual([addr(5)]);
  });
});

describe("a contract", () => {
  const info = {
    address: addr(5),
    contract_info: { code_id: "3", creator: addr(4), admin: "", label: "Vault ‮evil", created: { block_height: "120", tx_index: "1" }, ibc_port_id: "", extension: null },
  };

  it("TestLoadContract_reads_code_creator_admin_label_and_creation_block", async () => {
    const gw = new FakeGateway().on(queryPath("cosmwasm.wasm.v1.Query/ContractInfo", { address: addr(5) }), info);
    expect(await loadContract(client(gw), addr(5))).toEqual({
      address: addr(5),
      codeId: 3,
      creator: addr(4),
      admin: null,
      label: "Vault evil",
      createdHeight: 120,
    });
  });

  it("TestLoadContract_an_address_that_is_no_contract_is_null", async () => {
    expect(await loadContract(client(new FakeGateway()), addr(8))).toBeNull();
  });

  it("TestLoadHistory_decodes_the_message_of_each_operation_as_text", async () => {
    const gw = new FakeGateway().on(queryPath("cosmwasm.wasm.v1.Query/ContractHistory", { address: addr(5), ...first }), {
      entries: [
        { operation: "CONTRACT_CODE_HISTORY_OPERATION_TYPE_INIT", code_id: "3", updated: { block_height: "120", tx_index: "1" }, msg: b64('{"owner":"x"}') },
        { operation: "CONTRACT_CODE_HISTORY_OPERATION_TYPE_MIGRATE", code_id: "4", updated: null, msg: btoa("\u0001\u0002") },
      ],
    });
    const result = await loadHistory(client(gw), addr(5), page);
    expect(result.items[0]).toEqual({ operation: "init", codeId: 3, height: 120, message: '{"owner":"x"}' });
    expect(result.items[1]).toEqual({ operation: "migrate", codeId: 4, height: 0, message: null });
  });

  it("TestLoadState_shows_printable_keys_and_values_as_text_and_the_rest_by_size", async () => {
    const big = "x".repeat(300);
    const gw = new FakeGateway().on(queryPath("cosmwasm.wasm.v1.Query/AllContractState", { address: addr(5), ...first }), {
      models: [
        { key: b64("config"), value: b64('{"admin":"a"}') },
        { key: btoa("\u0000\u0001"), value: btoa("ÿþý") },
        { key: b64("big"), value: b64(big) },
      ],
      pagination: { next_key: "" },
    });
    const result = await loadState(client(gw), addr(5), page);
    expect(result.items[0]).toEqual({ key: "config", value: '{"admin":"a"}' });
    expect(result.items[1]).toEqual({ key: "0x0001", value: "3 bytes, not text" });
    expect(result.items[2]?.value).toBe(`${"x".repeat(160)}… (300 bytes)`);
  });

  it("TestLoadState_a_contract_with_no_state_is_an_empty_list", async () => {
    const gw = new FakeGateway().on(queryPath("cosmwasm.wasm.v1.Query/AllContractState", { address: addr(5), ...first }), { models: [], pagination: { next_key: "" } });
    expect(await loadState(client(gw), addr(5), page)).toEqual({ items: [], nextKey: null });
  });
});
