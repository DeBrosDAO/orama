import type { NodeDetail } from "../../model/nodes";
import { BlockLink, WalletLink } from "../../ui/links";
import { Card } from "../../ui/card";
import { RoleBadges, StatusBadge } from "../nodes/badges";

/** Who the node is: its id and name, state, roles, operator and hot key. */
export function NodeHeader({ detail }: { detail: NodeDetail }) {
  const { node, name } = detail;
  return (
    <Card>
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="break-all font-mono text-2xl font-semibold tracking-tight">{node.id}</h1>
        <StatusBadge status={node.status} />
      </div>
      {name !== null && (
        <p className="mt-1 text-sm text-muted">
          Name <span className="font-mono text-fg">{name}</span> (the operator claimed it for this node)
        </p>
      )}
      <div className="mt-3">
        <RoleBadges roles={node.roles} />
      </div>
      <dl className="mt-4 grid gap-x-8 gap-y-2 text-sm sm:grid-cols-[auto_1fr]">
        <dt className="text-muted">Operator</dt>
        <dd>
          <WalletLink wallet={{ address: node.operator }} />
        </dd>
        <dt className="text-muted">Hot key</dt>
        <dd>{node.hotKey === "" ? <span className="text-muted">none</span> : <WalletLink wallet={{ address: node.hotKey }} />}</dd>
        <dt className="text-muted">Registered at block</dt>
        <dd>
          <BlockLink height={node.registeredAtHeight} />
        </dd>
      </dl>
    </Card>
  );
}
