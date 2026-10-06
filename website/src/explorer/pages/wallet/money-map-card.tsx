import { Link } from "react-router";
import type { QueryState } from "../../data/use-query";
import { hashString } from "../../model/hash";
import { explorerPaths } from "../../model/routes";
import type { Counterparty } from "../../model/types";
import { shortAddress } from "../../model/units";
import { Card } from "../../ui/card";
import { HUE_RANGE } from "../../ui/identicon";
import { Query, Skeleton } from "../../ui/query-states";
import { MAP_LABEL_MAX_CHARS, MAP_SIZE } from "./constants";
import { layoutMoneyMap, MAP_CENTER_RADIUS, MAP_NODE_RADIUS } from "./money-map-layout";

const LABEL_OFFSET = 14;

function nodeLabel(c: Counterparty): string {
  const name = c.ref.label ?? shortAddress(c.ref.address);
  return name.length > MAP_LABEL_MAX_CHARS ? `${name.slice(0, MAP_LABEL_MAX_CHARS - 1)}…` : name;
}

function MoneyMap({ list }: { list: Counterparty[] }) {
  const { center, nodes } = layoutMoneyMap(list, MAP_SIZE);
  return (
    <svg viewBox={`0 0 ${MAP_SIZE.width} ${MAP_SIZE.height}`} className="mx-auto block w-full max-w-[400px]" role="group" aria-label="Money map: this wallet and the wallets it deals with most">
      {nodes.map((n) => (
        <line key={n.counterparty.ref.address} x1={center.x} y1={center.y} x2={n.x} y2={n.y} className="stroke-muted/60" strokeWidth={n.strokeWidth} />
      ))}
      <g aria-hidden="true">
        <circle cx={center.x} cy={center.y} r={MAP_CENTER_RADIUS} className="fill-fg" />
        <text x={center.x} y={center.y + 4} textAnchor="middle" fontSize="11" fontWeight="700" className="fill-bg">
          this
        </text>
      </g>
      {nodes.map((n) => {
        const ref = n.counterparty.ref;
        const hue = hashString(ref.address) % HUE_RANGE;
        return (
          <Link key={ref.address} to={explorerPaths.wallet(ref.address)} aria-label={`Open wallet ${ref.label ?? ref.address}`}>
            <g className="cursor-pointer">
              <circle cx={n.x} cy={n.y} r={MAP_NODE_RADIUS} fill={`hsl(${hue} 42% 40%)`} />
              <text x={n.x} y={n.y + MAP_NODE_RADIUS + LABEL_OFFSET} textAnchor="middle" fontSize="10" className="fill-muted">
                {nodeLabel(n.counterparty)}
              </text>
            </g>
          </Link>
        );
      })}
    </svg>
  );
}

export function MoneyMapCard({ state, onRetry }: { state: QueryState<Counterparty[]>; onRetry: () => void }) {
  return (
    <Card title="Money map">
      <Query state={state} onRetry={onRetry} loading={
          <div style={{ aspectRatio: `${MAP_SIZE.width} / ${MAP_SIZE.height}` }}>
            <Skeleton className="h-full w-full" />
          </div>
        }>
        {(list) =>
          list.length === 0 ? (
            <p className="py-4 text-sm text-muted">Nothing to draw yet. The map appears once this wallet deals with others.</p>
          ) : (
            <>
              <MoneyMap list={list} />
              <p className="mt-1.5 text-[12.5px] text-muted">Line thickness = ORAMA moved. Click a node to open that wallet.</p>
            </>
          )
        }
      </Query>
    </Card>
  );
}
