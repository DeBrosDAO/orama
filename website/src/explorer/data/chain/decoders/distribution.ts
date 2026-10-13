import { define, field, paramsUpdate, sp, table } from "./kit";

const MODULE = "Distribution";
const STAKING = { module: MODULE, category: "staking" } as const;

export const DISTRIBUTION_DECODERS = table(
  define("/cosmos.distribution.v1beta1.MsgSetWithdrawAddress", {
    ...STAKING,
    tag: "Set withdraw address",
    actor: "delegator_address",
    verb: ["set", "set"],
    object: (r) => sp`their reward withdraw address to ${r.party("withdraw_address")}`,
    fields: (r) => [field("Withdraw address", r.text("withdraw_address"), "address")],
  }),
  define("/cosmos.distribution.v1beta1.MsgWithdrawDelegatorReward", {
    ...STAKING,
    tag: "Withdraw rewards",
    actor: "delegator_address",
    verb: ["withdrew", "withdraw"],
    object: (r) => sp`staking rewards from ${r.party("validator_address")}`,
    fields: (r) => [field("Validator", r.text("validator_address"), "address")],
  }),
  define("/cosmos.distribution.v1beta1.MsgWithdrawValidatorCommission", {
    ...STAKING,
    tag: "Withdraw commission",
    actor: "validator_address",
    verb: ["withdrew", "withdraw"],
    object: () => ["its validator commission"],
  }),
  define("/cosmos.distribution.v1beta1.MsgFundCommunityPool", {
    module: MODULE,
    tag: "Fund community pool",
    category: "transfers",
    actor: "depositor",
    verb: ["funded", "fund"],
    object: (r) => sp`the community pool with ${r.coins("amount").parts}`,
    amount: (r) => r.coins("amount").norama,
  }),
  define("/cosmos.distribution.v1beta1.MsgDepositValidatorRewardsPool", {
    ...STAKING,
    tag: "Fund validator rewards",
    category: "transfers",
    actor: "depositor",
    verb: ["deposited", "deposit"],
    object: (r) => sp`${r.coins("amount").parts} into the rewards pool of ${r.party("validator_address")}`,
    amount: (r) => r.coins("amount").norama,
    fields: (r) => [field("Validator", r.text("validator_address"), "address")],
  }),
  define("/cosmos.distribution.v1beta1.MsgCommunityPoolSpend", {
    module: MODULE,
    tag: "Community pool spend",
    category: "transfers",
    actor: "authority",
    verb: ["spent", "spend"],
    object: (r) => sp`${r.coins("amount").parts} from the community pool on ${r.party("recipient")}`,
    amount: (r) => r.coins("amount").norama,
    fields: (r) => [field("Recipient", r.text("recipient"), "address")],
  }),
  paramsUpdate("/cosmos.distribution.v1beta1.MsgUpdateParams", MODULE, "distribution parameters"),
);
