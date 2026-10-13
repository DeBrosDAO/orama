import type { CodeInfo, CodePermission, ContractInfo, HistoryEntry, HistoryOperation, StateEntry } from "../../model/contracts";
import type { KeyPage, KeyQuery } from "../../model/paging";
import { groupedInteger } from "../../model/paging";
import { ChainReadError } from "./client";
import type { ChainClient } from "./client";
import { base64Bytes, base64Head, printableText, toHex } from "./bytes";
import { lookup, pagination, queryPath, readKeyPage } from "./module-query";
import { clean } from "./text";
import { account, optArr, optStr, optUint, rec, str, uint } from "./wire";

const CODES = "cosmwasm.wasm.v1.Query/Codes";
const CODE_INFO = "cosmwasm.wasm.v1.Query/CodeInfo";
const CONTRACT_INFO = "cosmwasm.wasm.v1.Query/ContractInfo";
const CONTRACTS_BY_CODE = "cosmwasm.wasm.v1.Query/ContractsByCode";
const CONTRACTS_BY_CREATOR = "cosmwasm.wasm.v1.Query/ContractsByCreator";
const CONTRACT_HISTORY = "cosmwasm.wasm.v1.Query/ContractHistory";
const ALL_CONTRACT_STATE = "cosmwasm.wasm.v1.Query/AllContractState";

const CHECKSUM_BYTES = 32;
const MAX_LABEL = 128;
const MAX_MESSAGE_BYTES = 2048;
const MAX_KEY_BYTES = 256;
/** How much of a stored value is read for the page: the rest is counted, not shown. */
const VALUE_PREVIEW_BYTES = 160;

const PERMISSIONS: Record<string, CodePermission> = {
  ACCESS_TYPE_EVERYBODY: "everybody",
  ACCESS_TYPE_NOBODY: "nobody",
  ACCESS_TYPE_ANY_OF_ADDRESSES: "any_of",
  ACCESS_TYPE_UNSPECIFIED: "unspecified",
};
const OPERATIONS: Record<string, HistoryOperation> = {
  CONTRACT_CODE_HISTORY_OPERATION_TYPE_INIT: "init",
  CONTRACT_CODE_HISTORY_OPERATION_TYPE_MIGRATE: "migrate",
  CONTRACT_CODE_HISTORY_OPERATION_TYPE_GENESIS: "genesis",
};

/** Code as CodeInfoResponse (the Codes list) and QueryCodeInfoResponse (CodeInfo) write it: the same facts under two names for the hash and permission. */
export function readCodeInfo(raw: unknown): CodeInfo {
  const c = rec(raw, "code");
  const access = rec(c.instantiate_permission, "code permission");
  const hash = base64Bytes(c.data_hash ?? c.checksum, "code checksum", CHECKSUM_BYTES);
  return {
    codeId: uint(c.code_id, "code id"),
    creator: account(c.creator, "code creator"),
    checksum: toHex(hash),
    permission: PERMISSIONS[optStr(access.permission, "code permission", 64)] ?? "unspecified",
    allowed: optArr(access.addresses, "code permission addresses").map((a) => account(a, "code permission address")),
  };
}

export async function loadCodes(client: ChainClient, query: KeyQuery): Promise<KeyPage<CodeInfo>> {
  const answer = await client.get(queryPath(CODES, { pagination: pagination(query) }));
  return readKeyPage(answer, "code_infos", "code list", readCodeInfo);
}

/** One stored code by id; null when none is stored under it. */
export async function loadCode(client: ChainClient, codeId: number): Promise<CodeInfo | null> {
  if (!Number.isSafeInteger(codeId) || codeId < 1) return null;
  const answer = await lookup(client, CODE_INFO, { code_id: codeId });
  if (answer === null) return null;
  const code = readCodeInfo(answer);
  if (code.codeId !== codeId) throw new ChainReadError("The chain sent a malformed code (another id than asked for).");
  return code;
}

function contractAddress(raw: unknown): string {
  return account(raw, "contract address");
}

/** The contracts instantiated from a code, in the order the chain keeps them. */
export async function loadCodeContracts(client: ChainClient, codeId: number, query: KeyQuery): Promise<KeyPage<string>> {
  const answer = await client.get(queryPath(CONTRACTS_BY_CODE, { code_id: codeId, pagination: pagination(query) }));
  return readKeyPage(answer, "contracts", "contract list", contractAddress);
}

/** The contracts an account instantiated. */
export async function loadCreatedContracts(client: ChainClient, creator: string, query: KeyQuery): Promise<KeyPage<string>> {
  const answer = await client.get(queryPath(CONTRACTS_BY_CREATOR, { creator_address: creator, pagination: pagination(query) }));
  return readKeyPage(answer, "contract_addresses", "contract list", contractAddress);
}

/** One contract by address; null when the address is not a contract. */
export async function loadContract(client: ChainClient, address: string): Promise<ContractInfo | null> {
  const answer = await lookup(client, CONTRACT_INFO, { address });
  if (answer === null) return null;
  const body = rec(answer, "contract");
  const info = rec(body.contract_info, "contract");
  const admin = optStr(info.admin, "contract admin", 120);
  const created = info.created === null || info.created === undefined ? null : rec(info.created, "contract creation");
  return {
    address: account(body.address, "contract address"),
    codeId: uint(info.code_id, "contract code"),
    creator: account(info.creator, "contract creator"),
    admin: admin === "" ? null : account(admin, "contract admin"),
    label: clean(optStr(info.label, "contract label", MAX_LABEL * 4), MAX_LABEL),
    createdHeight: created === null ? 0 : optUint(created.block_height, "contract creation height"),
  };
}

function readHistoryEntry(raw: unknown): HistoryEntry {
  const e = rec(raw, "contract history entry");
  const updated = e.updated === null || e.updated === undefined ? null : rec(e.updated, "contract history position");
  const msg = base64Bytes(e.msg, "contract message", MAX_MESSAGE_BYTES);
  return {
    operation: OPERATIONS[str(e.operation, "contract operation", 80)] ?? "unknown",
    codeId: uint(e.code_id, "contract code"),
    height: updated === null ? 0 : optUint(updated.block_height, "contract history height"),
    message: msg.length === 0 ? null : printableText(msg, MAX_MESSAGE_BYTES),
  };
}

/** The code a contract ran, from instantiation through each migration. */
export async function loadHistory(client: ChainClient, address: string, query: KeyQuery): Promise<KeyPage<HistoryEntry>> {
  const answer = await client.get(queryPath(CONTRACT_HISTORY, { address, pagination: pagination(query) }));
  return readKeyPage(answer, "entries", "contract history", readHistoryEntry);
}

function valueText(size: number, head: Uint8Array): string {
  const text = printableText(head, VALUE_PREVIEW_BYTES);
  const bytes = `${groupedInteger(String(size))} bytes`;
  if (text === null) return `${bytes}, not text`;
  return size > VALUE_PREVIEW_BYTES ? `${text}… (${bytes})` : text;
}

function readState(raw: unknown): StateEntry {
  const m = rec(raw, "contract state entry");
  const key = base64Bytes(m.key, "contract state key", MAX_KEY_BYTES);
  const value = base64Head(m.value, "contract state value", VALUE_PREVIEW_BYTES);
  return { key: printableText(key, MAX_KEY_BYTES) ?? `0x${toHex(key)}`, value: valueText(value.size, value.head) };
}

/** A contract's raw storage, in key order. A key is shown as text when it is printable, else as hex. */
export async function loadState(client: ChainClient, address: string, query: KeyQuery): Promise<KeyPage<StateEntry>> {
  const answer = await client.get(queryPath(ALL_CONTRACT_STATE, { address, pagination: pagination(query) }));
  return readKeyPage(answer, "models", "contract state", readState);
}
