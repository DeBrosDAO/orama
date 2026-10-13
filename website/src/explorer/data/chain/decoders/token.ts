import { define, field, sp, table } from "./kit";
import type { Reader, Verb } from "./kit";

const MODULE = "Tokens";
const TOKEN = { module: MODULE, actor: "sender" } as const;
const NORAMA = "norama";

const EXTENSIONS = [
  ["UNSPECIFIED", "unspecified"],
  ["MINT", "mint"],
  ["FREEZE", "freeze"],
  ["PERMANENT_DELEGATE", "permanent delegate"],
  ["TRANSFER_FEE", "transfer fee"],
  ["NON_TRANSFERABLE", "non-transferable"],
  ["PAUSE", "pause"],
  ["TRANSFER_HOOK", "transfer hook"],
] as const;

const denom = (r: Reader) => r.name("denom");
/** The ORAMA a message states: only when the token is norama itself. */
const noramaOf = (r: Reader) => (r.text("denom") === NORAMA ? r.norama("amount") : null);
const amountField = (r: Reader) => field("Amount", `${r.uint("amount")} ${denom(r)}`);

function capabilities(r: Reader): string {
  const features = [
    r.bool("mint") && "mint",
    r.bool("freeze") && "freeze",
    r.bool("pause") && "pause",
    r.bool("non_transferable") && "non-transferable",
  ].filter((f): f is string => f !== false);
  return features.join(", ");
}

export const TOKEN_DECODERS = table(
  define("/orama.token.v1.MsgCreateToken", {
    module: MODULE,
    actor: "creator",
    tag: "Create token",
    verb: ["created", "create"],
    object: (r) => sp`the token ${r.text("symbol") || r.name("subdenom")} (“${r.name("name")}”)`,
    fields: (r) => [
      field("Subdenom", r.text("subdenom")),
      field("Name", r.text("name")),
      field("Symbol", r.text("symbol")),
      field("Description", r.text("description")),
      field("Capabilities", capabilities(r)),
      field("Permanent delegate", r.text("permanent_delegate"), "address"),
      field("Transfer fee", r.digits("transfer_fee_bps") === "0" ? "" : r.bps("transfer_fee_bps")),
      field("Transfer hook", r.text("transfer_hook"), "address"),
    ],
  }),
  define("/orama.token.v1.MsgMint", {
    ...TOKEN,
    tag: "Mint token",
    verb: ["minted", "mint"],
    object: (r) => sp`${r.token("denom", "amount")} to ${r.party("recipient")}`,
    amount: noramaOf,
    fields: (r) => [field("Token", denom(r)), amountField(r), field("Recipient", r.text("recipient"), "address")],
  }),
  define("/orama.token.v1.MsgBurn", {
    ...TOKEN,
    tag: "Burn token",
    verb: ["burned", "burn"],
    object: (r) => sp`${r.token("denom", "amount")}`,
    amount: noramaOf,
    fields: (r) => [field("Token", denom(r)), amountField(r)],
  }),
  define("/orama.token.v1.MsgTransfer", {
    ...TOKEN,
    tag: "Transfer token",
    category: "transfers",
    verb: ["transferred", "transfer"],
    object: (r) =>
      r.text("from") === ""
        ? sp`${r.token("denom", "amount")} to ${r.party("to")}`
        : sp`${r.token("denom", "amount")} from ${r.party("from")} to ${r.party("to")}`,
    amount: noramaOf,
    fields: (r) => [
      field("Token", denom(r)),
      amountField(r),
      field("From", r.text("from"), "address"),
      field("To", r.text("to"), "address"),
    ],
  }),
  define("/orama.token.v1.MsgSetFrozen", {
    ...TOKEN,
    tag: "Freeze account",
    verb: (r): Verb => (r.bool("frozen") ? ["froze", "freeze"] : ["unfroze", "unfreeze"]),
    object: (r) => sp`${r.party("account")} for ${denom(r)}`,
    fields: (r) => [field("Token", denom(r)), field("Account", r.text("account"), "address"), field("Frozen", r.bool("frozen") ? "yes" : "no")],
  }),
  define("/orama.token.v1.MsgSetPaused", {
    ...TOKEN,
    tag: "Pause token",
    verb: (r): Verb => (r.bool("paused") ? ["paused", "pause"] : ["unpaused", "unpause"]),
    object: (r) => sp`${denom(r)}`,
    fields: (r) => [field("Token", denom(r)), field("Paused", r.bool("paused") ? "yes" : "no")],
  }),
  define("/orama.token.v1.MsgRenounce", {
    ...TOKEN,
    tag: "Renounce extension",
    verb: ["renounced", "renounce"],
    object: (r) => sp`the ${r.enumName("extension", "EXTENSION_", EXTENSIONS) || "unspecified"} extension of ${denom(r)}`,
    fields: (r) => [field("Token", denom(r)), field("Extension", r.enumName("extension", "EXTENSION_", EXTENSIONS))],
  }),
  define("/orama.token.v1.MsgSetShieldable", {
    ...TOKEN,
    tag: "Make token shieldable",
    verb: ["made", "make"],
    object: (r) => sp`${denom(r)} shieldable`,
    fields: (r) => [field("Token", denom(r))],
  }),
  define("/orama.token.v1.MsgDeleteToken", {
    ...TOKEN,
    tag: "Delete token",
    verb: ["deleted", "delete"],
    object: (r) => sp`the token ${denom(r)}`,
    fields: (r) => [field("Token", denom(r))],
  }),
);
