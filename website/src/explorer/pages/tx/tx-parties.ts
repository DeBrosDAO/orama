import { shortAddress } from "../../model/units";
import type { BalanceParty, TxMessage, ValidatorRef, WalletRef } from "../../model/types";

export interface FlowParty {
  /** "From", "To (validator)": what this party is in the movement. */
  role: string;
  wallet: WalletRef;
}

/** Who the money went from and to. `to` is null when a message has one actor and no movement. */
export interface Flow {
  from: FlowParty;
  to: FlowParty | null;
}

/** How a validator's operator wallet is named everywhere else: "val-1 operator". */
const OPERATOR_SUFFIX = " operator";

/** A validator's page is its operator's wallet, so the flow treats it as one. */
export function validatorAsWallet(v: ValidatorRef): WalletRef {
  return { address: v.operator, label: `${v.moniker}${OPERATOR_SUFFIX}` };
}

export function flowOf(message: TxMessage): Flow {
  switch (message.type) {
    case "send":
      return { from: { role: "From", wallet: message.from }, to: { role: "To", wallet: message.to } };
    case "delegate":
      return {
        from: { role: "From", wallet: message.delegator },
        to: { role: "To (validator)", wallet: validatorAsWallet(message.validator) },
      };
    case "undelegate":
    case "claim_rewards":
      return {
        from: { role: "From (validator)", wallet: validatorAsWallet(message.validator) },
        to: { role: "To", wallet: message.delegator },
      };
    case "storage_deal":
      return {
        from: { role: "From (owner)", wallet: message.owner },
        to: { role: "To (provider)", wallet: message.provider },
      };
    case "unknown":
      return { from: { role: "Signer", wallet: message.signer }, to: null };
  }
}

/** The name shown for a wallet in a flow node: its label, or a short address. */
export function walletTitle(wallet: WalletRef): string {
  return wallet.label ?? `Wallet ${shortAddress(wallet.address)}`;
}

const SYSTEM_LABELS = {
  burned: "Network (burned) 🔥",
  minted: "Newly issued rewards",
  staked: "Staked",
  unbonding: "Unbonding",
  storage_escrow: "Storage escrow",
} as const;

/** The plain name of a system pool in the balances table. */
export function systemPartyLabel(name: Extract<BalanceParty, { kind: "system" }>["name"]): string {
  return SYSTEM_LABELS[name];
}
