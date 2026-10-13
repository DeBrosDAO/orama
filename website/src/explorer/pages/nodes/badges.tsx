import type { BadgeTone } from "../../ui/badge";
import { Badge } from "../../ui/badge";
import { ROLE_INFO } from "../../model/nodes";
import type { NodeRole, NodeStatus } from "../../model/nodes";

const STATUS_TONE: Record<NodeStatus, BadgeTone> = {
  registered: "mute",
  active: "ok",
  jailed: "bad",
  retired: "mute",
  tombstoned: "bad",
};

const STATUS_TIP: Record<NodeStatus, string> = {
  registered: "Registered, with no role bonded at its minimum yet.",
  active: "At least one role is bonded at its minimum and the node is not jailed.",
  jailed: "The node was jailed and serves nothing until it is released.",
  retired: "The operator retired the node.",
  tombstoned: "The node was tombstoned for a serious fault and can never return.",
};

export function StatusBadge({ status }: { status: NodeStatus }) {
  return (
    <span title={STATUS_TIP[status]}>
      <Badge tone={STATUS_TONE[status]}>{status}</Badge>
    </span>
  );
}

export function RoleBadges({ roles }: { roles: readonly NodeRole[] }) {
  if (roles.length === 0) return <span className="text-muted">none</span>;
  return (
    <span className="flex flex-wrap gap-1">
      {roles.map((role) => (
        <span key={role} title={ROLE_INFO[role].blurb}>
          <Badge>{ROLE_INFO[role].label}</Badge>
        </span>
      ))}
    </span>
  );
}
