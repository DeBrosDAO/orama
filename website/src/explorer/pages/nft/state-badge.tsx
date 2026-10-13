import type { AssetState } from "../../model/nfts";
import { Badge } from "../../ui/badge";
import type { BadgeTone } from "../../ui/badge";

const STATES: Record<AssetState, { tone: BadgeTone; text: string }> = {
  compressed: { tone: "ok", text: "Compressed" },
  decompressed: { tone: "signal", text: "Decompressed" },
  burned: { tone: "bad", text: "Burned" },
};

/** Whether an asset is a leaf of a tree, an on-chain handle, or gone. */
export function StateBadge({ state }: { state: AssetState }) {
  return <Badge tone={STATES[state].tone}>{STATES[state].text}</Badge>;
}
