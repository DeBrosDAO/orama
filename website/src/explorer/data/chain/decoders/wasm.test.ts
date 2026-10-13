import { describe, expect, it } from "vitest";
import { ALICE, BOB, decode, fieldsOf, said } from "./fixtures";
import { entryPoint } from "./wasm";

const WASM = "/cosmwasm.wasm.v1.";
const norama = (amount: string) => ({ denom: "norama", amount });
/** A contract address is a 32-byte account; any valid orama address stands in. */
const CONTRACT = BOB;

describe("entryPoint", () => {
  it("TestEntryPoint_reads_the_one_key_of_an_object_a_json_string_or_base64_json", () => {
    expect(entryPoint({ transfer: { to: "x" } })).toBe("transfer");
    expect(entryPoint('{"mint":{}}')).toBe("mint");
    expect(entryPoint(btoa('{"burn":{"amount":"1"}}'))).toBe("burn");
  });

  it("TestEntryPoint_names_nothing_it_cannot_be_sure_of", () => {
    for (const bad of [null, undefined, 3, [], {}, { a: 1, b: 2 }, "not json", "{broken", btoa("[1,2]"), { [`${"x".repeat(60)}`]: 1 }]) {
      expect(entryPoint(bad)).toBe("");
    }
  });

  it("TestEntryPoint_strips_markup_and_control_characters", () => {
    expect(entryPoint({ "<img src=x onerror=1>": {} })).toBe("imgsrcxonerror1");
  });
});

describe("x/wasm decoders", () => {
  it("TestWasm_execute_names_the_contract_the_entry_point_and_the_funds", () => {
    const m = decode(`${WASM}MsgExecuteContract`, { sender: ALICE, contract: CONTRACT, msg: { swap: {} }, funds: [norama("2000000000")] });
    expect(said(m)).toBe("Alice called contract Bob (swap) with 2 ORAMA");
    expect(fieldsOf(m)).toEqual({ Contract: CONTRACT, "Entry point": "swap", Funds: "2 ORAMA" });
    expect(m.module).toBe("Contracts");
  });

  it("TestWasm_execute_without_funds_or_a_readable_message", () => {
    expect(said(decode(`${WASM}MsgExecuteContract`, { sender: ALICE, contract: CONTRACT }), true)).toBe("Alice tried to call contract Bob");
  });

  it("TestWasm_instantiate_and_store_code", () => {
    const inst = decode(`${WASM}MsgInstantiateContract`, { sender: ALICE, admin: BOB, code_id: "7", label: "my token", funds: [] });
    expect(said(inst)).toBe("Alice instantiated code #7 as “my token”");
    expect(fieldsOf(inst)).toEqual({ Code: "code #7", Label: "my token", Admin: BOB });
    const store = decode(`${WASM}MsgStoreCode`, { sender: ALICE, wasm_byte_code: "A".repeat(4000) });
    expect(said(store)).toBe("Alice uploaded contract code of 2.92 KiB (3,000 bytes)");
  });

  it("TestWasm_admin_and_migration_messages", () => {
    expect(said(decode(`${WASM}MsgMigrateContract`, { sender: ALICE, contract: CONTRACT, code_id: "9" }))).toBe("Alice migrated contract Bob to code #9");
    expect(said(decode(`${WASM}MsgUpdateAdmin`, { sender: ALICE, contract: CONTRACT, new_admin: ALICE }))).toBe("Alice changed the admin of contract Bob to Alice");
    expect(said(decode(`${WASM}MsgClearAdmin`, { sender: ALICE, contract: CONTRACT }))).toBe("Alice cleared the admin of contract Bob");
    expect(said(decode(`${WASM}MsgUpdateContractLabel`, { sender: ALICE, contract: CONTRACT, new_label: "v2" }))).toBe("Alice changed the label of contract Bob to “v2”");
  });

  it("TestWasm_authority_messages_name_the_authority", () => {
    expect(said(decode(`${WASM}MsgPinCodes`, { authority: BOB, code_ids: ["1", "2", "3"] }))).toBe("Bob pinned 3 codes in memory");
    expect(said(decode(`${WASM}MsgUpdateParams`, { authority: BOB }))).toBe("Bob updated the contract parameters");
    expect(said(decode(`${WASM}MsgSudoContract`, { authority: BOB, contract: CONTRACT }))).toBe("Bob called contract Bob with sudo rights");
  });

  it("TestWasm_a_message_with_no_account_field_is_attributed_to_the_transaction_signer", () => {
    expect(said(decode(`${WASM}MsgIBCCloseChannel`, { channel: "channel-4" }))).toBe("Alice closed IBC channel channel-4");
  });
});
