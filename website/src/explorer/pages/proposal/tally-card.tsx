import type { Proposal } from "../../model/governance";
import { Amount } from "../../ui/amount";
import { Card } from "../../ui/card";
import { Facts } from "../../ui/facts";

/** How the two houses have voted so far. The token house counts weight, the operator house one vote per operator identity. */
export function TallyCard({ proposal }: { proposal: Proposal }) {
  const { tokens, operators } = proposal;
  return (
    <Card title="Votes">
      <div className="grid gap-5 md:grid-cols-2">
        <div>
          <h3 className="mb-2 text-xs font-medium text-muted">Token house (weight of bonded stake)</h3>
          <Facts
            rows={[
              ["Yes", <Amount norama={tokens.yes} />],
              ["No", <Amount norama={tokens.no} />],
              ["Abstain", <Amount norama={tokens.abstain} />],
            ]}
          />
        </div>
        <div>
          <h3 className="mb-2 text-xs font-medium text-muted">Operator house (one vote each)</h3>
          <Facts
            rows={[
              ["Yes", <span className="tabular-nums">{operators.yes}</span>],
              ["No", <span className="tabular-nums">{operators.no}</span>],
              ["Abstain", <span className="tabular-nums">{operators.abstain}</span>],
              ["Eligible", <span className="tabular-nums">{operators.eligible}</span>],
            ]}
          />
        </div>
      </div>
      <p className="mt-3 text-xs text-muted">
        These are the proposal's own counts. A single voter's choice is on chain too, but only for an address that is named: the chain lists no voters.
      </p>
    </Card>
  );
}
