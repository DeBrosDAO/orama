import type { UnavailableEpochRow } from "../../model/epochs";
import { formatInt } from "../../model/units";
import { Badge } from "../../ui/badge";
import { Card } from "../../ui/card";

/** What a figure column says for an epoch the index could not read. Never a zero. */
export const NOT_AVAILABLE = "not available";

const NOT_AVAILABLE_WHY =
  "The chain's nodes keep only their recent states, and an epoch's figures are read from state. The index shows no figure for this epoch rather than a guess, and counts it as nothing in a total and as a gap in a chart.";

const LIST_NOTE =
  "An epoch marked not available closed while the node no longer kept its state (or follows one that did), so the index could not read its figures. Hover it for the reason. It is never shown as zero and adds nothing to a total.";

/** Under a list that has such a row. */
export function UnavailableNote({ className }: { className?: string }) {
  return <p className={className ?? "mt-3 text-[13px] text-muted"}>{LIST_NOTE}</p>;
}

/** "Not available: the node no longer kept ..." : the indexer's own reason, in a sentence. */
export function unavailableText(row: UnavailableEpochRow): string {
  return `Not available: ${row.reason}.`;
}

/** The marker in a list's figure cell; the reason is its tooltip. */
export function NotAvailable({ row }: { row: UnavailableEpochRow }) {
  return (
    <span className="text-muted" title={unavailableText(row)}>
      {NOT_AVAILABLE}
    </span>
  );
}

export function UnavailableBadge({ row, className }: { row: UnavailableEpochRow; className?: string }) {
  return (
    <span title={unavailableText(row)}>
      <Badge tone="mute" className={className}>
        {NOT_AVAILABLE}
      </Badge>
    </span>
  );
}

/** "Epoch 5" or "Epochs 5 to 7": the epochs of a gap. */
export function gapEpochs(from: number, to: number): string {
  return from === to ? `epoch ${formatInt(from)}` : `epochs ${formatInt(from)} to ${formatInt(to)}`;
}

/** The epoch page of an epoch with no figures: that it has none, why, and what that does not mean. */
export function UnavailableCard({ row }: { row: UnavailableEpochRow }) {
  return (
    <Card title="Figures not available">
      <p className="text-sm">{unavailableText(row)}</p>
      <p className="mt-2 text-[13px] text-muted">{NOT_AVAILABLE_WHY}</p>
    </Card>
  );
}
