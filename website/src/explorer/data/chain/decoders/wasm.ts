import type { Rec } from "../wire";
import { MAX_RAW } from "./format";
import { define, field, paramsUpdate, plural, sp, table } from "./kit";
import type { Reader } from "./kit";

const MODULE = "Contracts";
const WASM = { module: MODULE, actor: "sender" } as const;
const AUTHORITY = { module: MODULE, actor: "authority" } as const;
const MAX_ENTRY_POINT = 40;
const MAX_CODE_IDS = 6;

/**
 * The name of the entry point a contract message calls: the one key of its
 * JSON object, such as "transfer". The message may arrive as a JSON object or
 * as a base64 or plain string of JSON; anything else has no name to show.
 */
export function entryPoint(msg: unknown): string {
  let value: unknown = msg;
  if (typeof msg === "string" && msg.length <= MAX_RAW) {
    try {
      value = JSON.parse(msg.startsWith("{") ? msg : atob(msg));
    } catch {
      return "";
    }
  }
  if (typeof value !== "object" || value === null || Array.isArray(value)) return "";
  const keys = Object.keys(value as Rec);
  const key = keys.length === 1 ? (keys[0] ?? "") : "";
  return key.length <= MAX_ENTRY_POINT ? key.replace(/[^\w.-]/g, "") : "";
}

const contract = (r: Reader) => r.party("contract");
const code = (r: Reader) => `code #${r.uint("code_id")}`;
const funds = (r: Reader) => (r.count("funds") === 0 ? "" : r.coinsText("funds"));
const codeIds = (r: Reader) => {
  const ids = r.items("code_ids").map((id) => String(id));
  const more = r.count("code_ids") - Math.min(ids.length, MAX_CODE_IDS);
  return `${ids.slice(0, MAX_CODE_IDS).join(", ")}${more > 0 ? `, +${more} more` : ""}`;
};

const entry = (r: Reader) => entryPoint(r.raw("msg"));

export const WASM_DECODERS = table(
  define("/cosmwasm.wasm.v1.MsgStoreCode", {
    ...WASM,
    tag: "Upload contract code",
    verb: ["uploaded", "upload"],
    object: (r) => sp`contract code of ${r.blobSize("wasm_byte_code")}`,
    fields: (r) => [field("Code size", r.blobSize("wasm_byte_code"))],
  }),
  define("/cosmwasm.wasm.v1.MsgInstantiateContract", {
    ...WASM,
    tag: "Instantiate contract",
    verb: ["instantiated", "instantiate"],
    object: (r) => sp`${code(r)} as “${r.name("label")}”${funds(r) === "" ? "" : ` with ${funds(r)}`}`,
    fields: (r) => [field("Code", code(r)), field("Label", r.text("label")), field("Admin", r.text("admin"), "address"), field("Funds", funds(r))],
  }),
  define("/cosmwasm.wasm.v1.MsgInstantiateContract2", {
    ...WASM,
    tag: "Instantiate contract",
    verb: ["instantiated", "instantiate"],
    object: (r) => sp`${code(r)} as “${r.name("label")}” at a salted address${funds(r) === "" ? "" : ` with ${funds(r)}`}`,
    fields: (r) => [field("Code", code(r)), field("Label", r.text("label")), field("Admin", r.text("admin"), "address"), field("Funds", funds(r))],
  }),
  define("/cosmwasm.wasm.v1.MsgExecuteContract", {
    ...WASM,
    tag: "Call contract",
    verb: ["called", "call"],
    object: (r) => sp`contract ${contract(r)}${entry(r) === "" ? "" : ` (${entry(r)})`}${funds(r) === "" ? "" : ` with ${funds(r)}`}`,
    fields: (r) => [field("Contract", r.text("contract"), "address"), field("Entry point", entry(r)), field("Funds", funds(r))],
  }),
  define("/cosmwasm.wasm.v1.MsgMigrateContract", {
    ...WASM,
    tag: "Migrate contract",
    verb: ["migrated", "migrate"],
    object: (r) => sp`contract ${contract(r)} to ${code(r)}`,
    fields: (r) => [field("Contract", r.text("contract"), "address"), field("Code", code(r))],
  }),
  define("/cosmwasm.wasm.v1.MsgUpdateAdmin", {
    ...WASM,
    tag: "Change contract admin",
    verb: ["changed", "change"],
    object: (r) => sp`the admin of contract ${contract(r)} to ${r.party("new_admin")}`,
    fields: (r) => [field("Contract", r.text("contract"), "address"), field("New admin", r.text("new_admin"), "address")],
  }),
  define("/cosmwasm.wasm.v1.MsgClearAdmin", {
    ...WASM,
    tag: "Clear contract admin",
    verb: ["cleared", "clear"],
    object: (r) => sp`the admin of contract ${contract(r)}`,
    fields: (r) => [field("Contract", r.text("contract"), "address")],
  }),
  define("/cosmwasm.wasm.v1.MsgUpdateContractLabel", {
    ...WASM,
    tag: "Change contract label",
    verb: ["changed", "change"],
    object: (r) => sp`the label of contract ${contract(r)} to “${r.name("new_label")}”`,
    fields: (r) => [field("Contract", r.text("contract"), "address"), field("New label", r.text("new_label"))],
  }),
  define("/cosmwasm.wasm.v1.MsgUpdateInstantiateConfig", {
    ...WASM,
    tag: "Change instantiate permission",
    verb: ["changed", "change"],
    object: (r) => sp`who may instantiate ${code(r)}`,
    fields: (r) => [field("Code", code(r)), field("Permission", r.at("new_instantiate_permission").text("permission"))],
  }),
  define("/cosmwasm.wasm.v1.MsgSudoContract", {
    ...AUTHORITY,
    tag: "Sudo contract",
    verb: ["called", "call"],
    object: (r) => sp`contract ${contract(r)} with sudo rights`,
    fields: (r) => [field("Contract", r.text("contract"), "address")],
  }),
  define("/cosmwasm.wasm.v1.MsgPinCodes", {
    ...AUTHORITY,
    tag: "Pin contract code",
    verb: ["pinned", "pin"],
    object: (r) => sp`${plural(r.count("code_ids"), "code")} in memory`,
    fields: (r) => [field("Code IDs", codeIds(r))],
  }),
  define("/cosmwasm.wasm.v1.MsgUnpinCodes", {
    ...AUTHORITY,
    tag: "Unpin contract code",
    verb: ["unpinned", "unpin"],
    object: (r) => sp`${plural(r.count("code_ids"), "code")} from memory`,
    fields: (r) => [field("Code IDs", codeIds(r))],
  }),
  define("/cosmwasm.wasm.v1.MsgStoreAndInstantiateContract", {
    ...AUTHORITY,
    tag: "Upload and instantiate contract",
    verb: ["uploaded and instantiated", "upload and instantiate"],
    object: (r) => sp`contract code of ${r.blobSize("wasm_byte_code")} as “${r.name("label")}”`,
    fields: (r) => [field("Code size", r.blobSize("wasm_byte_code")), field("Label", r.text("label")), field("Admin", r.text("admin"), "address"), field("Funds", funds(r))],
  }),
  define("/cosmwasm.wasm.v1.MsgStoreAndMigrateContract", {
    ...AUTHORITY,
    tag: "Upload and migrate contract",
    verb: ["uploaded and migrated", "upload and migrate"],
    object: (r) => sp`contract code of ${r.blobSize("wasm_byte_code")} and migrated contract ${contract(r)} to it`,
    fields: (r) => [field("Code size", r.blobSize("wasm_byte_code")), field("Contract", r.text("contract"), "address")],
  }),
  define("/cosmwasm.wasm.v1.MsgAddCodeUploadParamsAddresses", {
    ...AUTHORITY,
    tag: "Allow code upload",
    verb: ["allowed", "allow"],
    object: (r) => sp`${plural(r.count("addresses"), "address", "addresses")} to upload contract code`,
    fields: (r) => [field("Addresses", r.list("addresses"))],
  }),
  define("/cosmwasm.wasm.v1.MsgRemoveCodeUploadParamsAddresses", {
    ...AUTHORITY,
    tag: "Disallow code upload",
    verb: ["removed", "remove"],
    object: (r) => sp`${plural(r.count("addresses"), "address", "addresses")} from those who may upload contract code`,
    fields: (r) => [field("Addresses", r.list("addresses"))],
  }),
  define("/cosmwasm.wasm.v1.MsgIBCSend", {
    module: MODULE,
    actor: "",
    tag: "IBC send",
    verb: ["sent", "send"],
    object: (r) => sp`an IBC packet on channel ${r.name("channel")}`,
    fields: (r) => [field("Channel", r.text("channel"))],
  }),
  define("/cosmwasm.wasm.v1.MsgIBCCloseChannel", {
    module: MODULE,
    actor: "",
    tag: "IBC close channel",
    verb: ["closed", "close"],
    object: (r) => sp`IBC channel ${r.name("channel")}`,
    fields: (r) => [field("Channel", r.text("channel"))],
  }),
  paramsUpdate("/cosmwasm.wasm.v1.MsgUpdateParams", MODULE, "contract parameters"),
);
