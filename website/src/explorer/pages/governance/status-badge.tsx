import type { ProposalStatus } from "../../model/governance";
import { statusText } from "../../model/governance";
import { Badge } from "../../ui/badge";
import type { BadgeTone } from "../../ui/badge";

const TONES: Record<ProposalStatus, BadgeTone> = {
  voting: "signal",
  veto_window: "signal",
  timelock: "signal",
  executed: "ok",
  rejected: "bad",
  failed: "bad",
  unspecified: "mute",
};

/** Where a proposal is in its life. */
export function StatusBadge({ status }: { status: ProposalStatus }) {
  return <Badge tone={TONES[status]}>{statusText(status)}</Badge>;
}
