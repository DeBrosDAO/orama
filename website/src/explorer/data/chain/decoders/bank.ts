import type { TxMessage } from "../../../model/types";
import { account, arr, digits, rec } from "../wire";
import type { Rec } from "../wire";
import { define, field, paramsUpdate, plural, sp, table, walletRef } from "./kit";
import type { Decoder, Env } from "./kit";

const TYPE_SEND = "/cosmos.bank.v1beta1.MsgSend";
const NORAMA = "norama";
const MODULE = "Bank";

/** The norama in a coin list, or null unless every coin is norama: a mixed or foreign send has no single ORAMA amount. */
function onlyNorama(coins: unknown): string | null {
  const list = arr(coins, "send amount");
  let total = 0n;
  for (const item of list) {
    const c = rec(item, "send amount");
    if (c.denom !== NORAMA) return null;
    total += BigInt(digits(c.amount, "send amount"));
  }
  return list.length === 0 ? null : total.toString();
}

function mapSend(m: Rec, env: Env): TxMessage | null {
  const amount = onlyNorama(m.amount);
  if (amount === null) return null;
  return {
    type: "send",
    from: walletRef(account(m.from_address, "sender"), env),
    to: walletRef(account(m.to_address, "receiver"), env),
    amount,
  };
}

const [, sendAction] = define(TYPE_SEND, {
  module: MODULE,
  tag: "Transfer",
  category: "transfers",
  actor: "from_address",
  verb: ["sent", "send"],
  object: (r) => sp`${r.coins("amount").parts} to ${r.party("to_address")}`,
  amount: (r) => r.coins("amount").norama,
  fields: (r) => [field("To", r.text("to_address"), "address")],
});

/** A send of ORAMA alone is a transfer; any other coin is shown as the coins it names. */
const send: Decoder = (m, env) => mapSend(m, env) ?? sendAction(m, env);

export const BANK_DECODERS = table(
  [TYPE_SEND, send],
  define("/cosmos.bank.v1beta1.MsgMultiSend", {
    module: MODULE,
    tag: "Multi-send",
    category: "transfers",
    actor: "",
    verb: ["made", "make"],
    object: (r) => sp`a multi-send from ${plural(r.count("inputs"), "input")} to ${plural(r.count("outputs"), "output")}`,
    fields: (r) => [field("Inputs", String(r.count("inputs"))), field("Outputs", String(r.count("outputs")))],
  }),
  define("/cosmos.bank.v1beta1.MsgSetSendEnabled", {
    module: MODULE,
    tag: "Set send enabled",
    actor: "authority",
    verb: ["changed", "change"],
    object: (r) => sp`which tokens may be sent (${plural(r.count("send_enabled"), "setting")})`,
  }),
  paramsUpdate("/cosmos.bank.v1beta1.MsgUpdateParams", MODULE, "bank parameters"),
);
