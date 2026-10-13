import { Lock } from "lucide-react";
import type { Privacy } from "../model/shielded";
import { Badge } from "./badge";

const LABELS: Record<Exclude<Privacy, "public">, string> = {
  private: "Private",
  boundary: "Crosses the shielded pool",
};

/**
 * Says how much of a transaction the chain makes public. A private transfer is "Private": its
 * sender, receiver and amount are not on chain. Shielding and unshielding are the public edge of
 * the pool, so they say that instead. An ordinary transaction has no badge unless `showPublic`.
 */
export function PrivacyBadge({ privacy, showPublic = false }: { privacy: Privacy; showPublic?: boolean }) {
  if (privacy === "public") return showPublic ? <Badge tone="mute">Public</Badge> : null;
  return (
    <Badge tone="signal">
      <Lock size={11} aria-hidden="true" />
      {LABELS[privacy]}
    </Badge>
  );
}
