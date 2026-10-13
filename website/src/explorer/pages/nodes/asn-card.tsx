import { asnShares } from "../../model/nodes";
import type { ChainNode } from "../../model/nodes";
import { formatPct } from "../../model/units";
import { Card } from "../../ui/card";

const MAX_BARS = 8;

/** How the live nodes spread over the autonomous systems their operators declared. */
export function AsnCard({ nodes }: { nodes: readonly ChainNode[] }) {
  const shares = asnShares(nodes);
  const shown = shares.slice(0, MAX_BARS);
  const rest = shares.slice(MAX_BARS);
  return (
    <Card title="Spread over networks">
      {shares.length === 0 ? (
        <p className="text-sm text-muted">No live node to count.</p>
      ) : (
        <ul className="space-y-2">
          {shown.map((s) => (
            <li key={s.asn}>
              <div className="flex items-baseline justify-between gap-3 text-sm">
                <span>{s.asn === 0 ? "No ASN declared" : `AS${s.asn}`}</span>
                <span className="font-mono tabular-nums text-muted">
                  {s.nodes} node{s.nodes === 1 ? "" : "s"} · {formatPct(s.share)}
                </span>
              </div>
              <div className="mt-1 h-[5px] overflow-hidden rounded-[3px] bg-surface-3">
                <div className="h-full bg-fg/60" style={{ width: `${Math.min(100, s.share * 100)}%` }} />
              </div>
            </li>
          ))}
        </ul>
      )}
      {rest.length > 0 && (
        <p className="mt-2 text-[13px] text-muted">
          And {rest.length} more network{rest.length === 1 ? "" : "s"} with {rest.reduce((n, s) => n + s.nodes, 0)} nodes between them.
        </p>
      )}
      <p className="mt-3 text-[13px] text-muted">
        Operators declare their ASN and the chain cannot check it. The more networks the nodes spread over, the fewer nodes one provider&apos;s outage
        takes down.
      </p>
    </Card>
  );
}
