import type { BalanceChange } from "../../model/types";
import { formatNoramaFixed, MINUS, NORAMA_PER_ORAMA, parseNorama } from "../../model/units";

/** Decimal places in the balances table, so every column lines up. */
export const BALANCE_DECIMALS = 4;

const SMALLEST_SHOWN = NORAMA_PER_ORAMA / 10n ** BigInt(BALANCE_DECIMALS);
const SMALLEST_SHOWN_TEXT = `<${formatNoramaFixed(SMALLEST_SHOWN.toString(), BALANCE_DECIMALS)}`;
const ZERO_TEXT = formatNoramaFixed("0", BALANCE_DECIMALS);

export type ChangeTone = "gain" | "signal" | "plain";

/** A balance, or an em dash for a system pool that has none worth showing. */
export function balanceText(amount: string | null): string {
  return amount === null ? "—" : formatNoramaFixed(amount, BALANCE_DECIMALS);
}

function magnitudeText(abs: bigint): string {
  if (abs > 0n && abs < SMALLEST_SHOWN) return SMALLEST_SHOWN_TEXT;
  return formatNoramaFixed(abs.toString(), BALANCE_DECIMALS);
}

function isBurnedPool(change: BalanceChange): boolean {
  return change.party.kind === "system" && change.party.name === "burned";
}

/** The change as text. Burned ORAMA is shown as a plain amount, everything else with its sign (a pool that gave value reads negative). */
export function changeText(change: BalanceChange): string {
  const delta = parseNorama(change.delta);
  const abs = delta < 0n ? -delta : delta;
  if (delta === 0n) return ZERO_TEXT;
  if (isBurnedPool(change)) return `${magnitudeText(abs)} 🔥`;
  return `${delta < 0n ? MINUS : "+"}${magnitudeText(abs)}`;
}

/** Wallets that received value read as a gain. A system pool is bookkeeping, not a winner, so it is never green. */
export function changeTone(change: BalanceChange): ChangeTone {
  if (isBurnedPool(change)) return "signal";
  if (change.party.kind === "system") return "plain";
  return parseNorama(change.delta) > 0n ? "gain" : "plain";
}

/** True when every change together nets to zero: value only moved, none appeared or vanished. */
export function deltasSumToZero(changes: BalanceChange[]): boolean {
  return changes.reduce((sum, c) => sum + parseNorama(c.delta), 0n) === 0n;
}

/** Stable React key for a row: a wallet address or a system pool name. */
export function changeKey(change: BalanceChange): string {
  return change.party.kind === "wallet" ? change.party.ref.address : `system:${change.party.name}`;
}
