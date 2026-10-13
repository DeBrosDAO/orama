import { parseNorama } from "../../model/units";
import type { WalletBalance } from "../../model/types";

export type SplitId = "available" | "staked" | "unbonding";

export interface SplitSegment {
  id: SplitId;
  label: string;
  norama: string;
  /** Share of the total, 0 to 100. */
  percent: number;
  /** Width to draw: the share, but never so thin a non-zero part disappears. */
  width: number;
}

export const MIN_SEGMENT_WIDTH = 1.5;

const PERCENT_SCALE = 10_000n;

/**
 * The three parts of a balance with their share of the total.
 */
export function splitSegments(balance: WalletBalance): SplitSegment[] {
  const parts: { id: SplitId; label: string; norama: string }[] = [
    { id: "available", label: "Available", norama: balance.available },
    { id: "staked", label: "Staked", norama: balance.staked },
    { id: "unbonding", label: "Unbonding", norama: balance.unbonding },
  ];
  const total = parts.reduce((sum, p) => sum + parseNorama(p.norama), 0n);
  return parts.map((p) => {
    const amount = parseNorama(p.norama);
    const percent = total === 0n ? 0 : Number((amount * PERCENT_SCALE) / total) / Number(PERCENT_SCALE / 100n);
    return { ...p, percent, width: amount === 0n ? 0 : Math.max(percent, MIN_SEGMENT_WIDTH) };
  });
}
