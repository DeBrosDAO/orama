import type { TxMessage } from "../../../model/types";
import { ChainReadError } from "../client";
import { account, digits, rec, str } from "../wire";
import type { Rec } from "../wire";
import { define, field, operatorAccount, paramsUpdate, sp, table, walletRef } from "./kit";
import type { Decoder, Env, Reader } from "./kit";

const MODULE = "Staking";
const NORAMA = "norama";
const TYPE_DELEGATE = "/cosmos.staking.v1beta1.MsgDelegate";
const TYPE_UNDELEGATE = "/cosmos.staking.v1beta1.MsgUndelegate";
/** The staking module's marker for a description field the edit leaves as it is. */
const DO_NOT_MODIFY = "[do-not-modify]";
const STAKING = { module: MODULE, category: "staking" } as const;

function mapStake(kind: "delegate" | "undelegate", m: Rec, env: Env): TxMessage | null {
  const coin = rec(m.amount, "stake amount");
  if (coin.denom !== NORAMA) return null;
  const operator = operatorAccount(str(m.validator_address, "validator address", 120));
  if (operator === null) throw new ChainReadError("The chain sent a malformed validator address.");
  return {
    type: kind,
    delegator: walletRef(account(m.delegator_address, "delegator"), env),
    validator: env.validator(operator),
    amount: digits(coin.amount, "stake amount"),
  };
}

const coinOf = (r: Reader, key = "amount") => r.at(key).token("denom", "amount");
const noramaOf = (r: Reader, key = "amount") => (r.at(key).text("denom") === NORAMA ? r.at(key).norama("amount") : null);
/** A description field the edit changes; the sentinel and empty mean "unchanged". */
const changed = (r: Reader, key: string) => {
  const v = r.at("description").text(key);
  return v === DO_NOT_MODIFY ? "" : v;
};

/** A delegation of ORAMA is a stake message; one in any other token is shown as the coin it names. */
const stake = (typeUrl: string, tag: string, verb: readonly [string, string], preposition: string): readonly [string, Decoder] => {
  const [, action] = define(typeUrl, {
    ...STAKING,
    tag,
    actor: "delegator_address",
    verb,
    object: (r) => sp`${coinOf(r)} ${preposition} ${r.party("validator_address")}`,
    amount: (r) => noramaOf(r),
    fields: (r) => [field("Validator", r.text("validator_address"), "address")],
  });
  const strict = typeUrl === TYPE_DELEGATE ? "delegate" : "undelegate";
  return [typeUrl, (m, env) => mapStake(strict, m, env) ?? action(m, env)];
};

export const STAKING_DECODERS = table(
  stake(TYPE_DELEGATE, "Delegate", ["delegated", "delegate"], "to"),
  stake(TYPE_UNDELEGATE, "Undelegate", ["undelegated", "undelegate"], "from"),
  define("/cosmos.staking.v1beta1.MsgCreateValidator", {
    ...STAKING,
    tag: "Create validator",
    actor: "delegator_address",
    verb: ["created", "create"],
    object: (r) => sp`the validator “${r.at("description").name("moniker")}” with a self-delegation of ${coinOf(r, "value")}`,
    amount: (r) => noramaOf(r, "value"),
    fields: (r) => [
      field("Moniker", r.at("description").text("moniker")),
      field("Validator address", r.text("validator_address"), "address"),
      field("Commission rate", r.at("commission").text("rate")),
      field("Maximum commission rate", r.at("commission").text("max_rate")),
      field("Maximum commission change", r.at("commission").text("max_change_rate")),
      field("Minimum self-delegation", r.text("min_self_delegation")),
    ],
  }),
  define("/cosmos.staking.v1beta1.MsgEditValidator", {
    ...STAKING,
    tag: "Edit validator",
    actor: "validator_address",
    verb: ["edited", "edit"],
    object: () => ["its validator settings"],
    fields: (r) => [
      field("Moniker", changed(r, "moniker")),
      field("Website", changed(r, "website")),
      field("Details", changed(r, "details")),
      field("Commission rate", r.text("commission_rate")),
      field("Minimum self-delegation", r.text("min_self_delegation")),
    ],
  }),
  define("/cosmos.staking.v1beta1.MsgBeginRedelegate", {
    ...STAKING,
    tag: "Redelegate",
    actor: "delegator_address",
    verb: ["moved", "move"],
    object: (r) => sp`${coinOf(r)} of stake from ${r.party("validator_src_address")} to ${r.party("validator_dst_address")}`,
    amount: (r) => noramaOf(r),
    fields: (r) => [
      field("From validator", r.text("validator_src_address"), "address"),
      field("To validator", r.text("validator_dst_address"), "address"),
    ],
  }),
  define("/cosmos.staking.v1beta1.MsgCancelUnbondingDelegation", {
    ...STAKING,
    tag: "Cancel unbonding",
    actor: "delegator_address",
    verb: ["cancelled", "cancel"],
    object: (r) => sp`the unbonding of ${coinOf(r)} from ${r.party("validator_address")}`,
    amount: (r) => noramaOf(r),
    fields: (r) => [field("Validator", r.text("validator_address"), "address"), field("Unbonding created at height", r.int("creation_height"))],
  }),
  paramsUpdate("/cosmos.staking.v1beta1.MsgUpdateParams", MODULE, "staking parameters"),
);
