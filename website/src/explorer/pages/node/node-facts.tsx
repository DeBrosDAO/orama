import { hasRole, utilisation } from "../../model/nodes";
import type { NodeDetail } from "../../model/nodes";
import { formatBytes, formatPct } from "../../model/units";
import { Amount } from "../../ui/amount";
import { Card } from "../../ui/card";

/** What the operator declared about the node: where it is reachable, where it says it runs, what it offers. */
export function NodeFacts({ detail }: { detail: NodeDetail }) {
  const { node, params } = detail;
  const used = utilisation(node);
  return (
    <Card title="Declared by the operator">
      <dl className="grid gap-x-8 gap-y-2 text-sm sm:grid-cols-[auto_1fr]">
        <dt className="text-muted">Endpoints</dt>
        <dd>
          {node.endpoints.length === 0 ? (
            <span className="text-muted">none declared</span>
          ) : (
            <ul className="space-y-0.5">
              {node.endpoints.map((e) => (
                <li key={e} className="break-all font-mono text-[0.92em]">{e}</li>
              ))}
            </ul>
          )}
        </dd>
        <dt className="text-muted">Network (ASN)</dt>
        <dd>{node.asn === 0 ? <span className="text-muted">none declared</span> : `AS${node.asn}`}</dd>
        <dt className="text-muted">Region</dt>
        <dd>{node.regionHint === "" ? <span className="text-muted">none declared</span> : node.regionHint}</dd>
        <dt className="text-muted">Service keys bound</dt>
        <dd>{node.services.length === 0 ? <span className="text-muted">none</span> : node.services.join(", ")}</dd>
        {hasRole(node, "storage") && (
          <>
            <dt className="text-muted">Storage declared</dt>
            <dd>{formatBytes(node.declaredCapacityBytes)}</dd>
            <dt className="text-muted">Reserved for deals</dt>
            <dd>
              {formatBytes(node.reservedCapacityBytes)}
              {used !== null && <span className="ml-2 text-muted">({formatPct(used)} of what it declared)</span>}
            </dd>
            <dt className="text-muted">Bond per GiB</dt>
            <dd>
              <Amount norama={params.bondPerGib} maxFraction={2} /> of storage bond backs each GiB declared
            </dd>
          </>
        )}
      </dl>
      <p className="mt-3 text-[13px] text-muted">
        The chain stores these as the operator wrote them and cannot check them: the ASN, the region and the endpoints show what the operator says, not what was measured.
      </p>
    </Card>
  );
}
