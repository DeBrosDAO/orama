import { Building2, Home, Power, Server, Store, Warehouse } from "lucide-react";
import { Diagram, Edge, Label, NodeDot } from "./diagram";

/**
 * The problem in one picture. Left: every app inside one company's building,
 * one power switch. Right: the same apps spread over machines with different
 * owners, linked to each other, no switch.
 */

const botSize = 22;

const APP_GRID: [number, number][] = [];
for (let row = 0; row < 3; row++) {
  for (let col = 0; col < 4; col++) {
    APP_GRID.push([64 + col * 30, 92 + row * 30]);
  }
}

function TodayCloud() {
  return (
    <Diagram viewBox="0 0 240 240" label="Today: every app inside one company, behind one switch">
      <rect x={30} y={50} width={180} height={140} rx={6} className="stroke-accent/50 fill-surface" strokeWidth={1} />
      <Building2 x={36} y={56} width={16} height={16} className="text-accent" strokeWidth={1.5} />
      <Label x={120} y={67} tone="bright">One company</Label>
      {APP_GRID.map(([x, y]) => (
        <rect key={`${x}-${y}`} x={x} y={y} width={botSize} height={botSize} rx={2} className="fill-surface-2 stroke-border" strokeWidth={1} />
      ))}
      <circle cx={120} cy={214} r={14} className="fill-surface-2 stroke-fg" strokeWidth={1} />
      <Power x={111} y={205} width={18} height={18} className="text-fg" strokeWidth={1.75} />
      <line x1={120} y1={190} x2={120} y2={200} className="stroke-fg/60" strokeWidth={1} strokeDasharray="2 3" />
    </Diagram>
  );
}


const OWNERS: { at: [number, number]; icon: typeof Home }[] = [
  { at: [60, 60], icon: Home },
  { at: [150, 40], icon: Server },
  { at: [205, 105], icon: Store },
  { at: [175, 185], icon: Home },
  { at: [85, 200], icon: Warehouse },
  { at: [35, 135], icon: Server },
  { at: [120, 120], icon: Home },
];

const OWNER_LINKS: [number, number][] = [
  [0, 1], [1, 2], [2, 3], [3, 4], [4, 5], [5, 0],
  [6, 0], [6, 2], [6, 4], [6, 1], [6, 3], [6, 5],
];

function OramaCloud() {
  return (
    <Diagram viewBox="0 0 240 240" label="Orama: the same apps spread over machines meant to have many owners, no single switch">
      {OWNER_LINKS.map(([a, b], i) => (
        <Edge key={`${a}-${b}`} from={OWNERS[a].at} to={OWNERS[b].at} flowing={i % 3 === 0} />
      ))}
      {OWNERS.map(({ at, icon }, i) => (
        <NodeDot key={i} x={at[0]} y={at[1]} icon={icon} tone={i === 6 ? "bright" : "normal"} />
      ))}
    </Diagram>
  );
}

export function CloudCompare() {
  return (
    <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
      <figure className="flex flex-col items-center gap-4 border border-dashed border-border p-6 sm:p-8">
        <span className="font-mono text-[11px] tracking-[0.25em] uppercase text-muted">Today</span>
        <div className="w-full max-w-[280px]">
          <TodayCloud />
        </div>
        <figcaption className="text-center">
          <p className="font-display font-semibold text-fg text-lg">One owner. One switch.</p>
          <p className="text-sm text-muted">An outage or a policy change, and every app goes dark.</p>
        </figcaption>
      </figure>
      <figure className="flex flex-col items-center gap-4 border border-dashed border-fg/25 p-6 sm:p-8 bg-white/[0.015]">
        <span className="font-mono text-[11px] tracking-[0.25em] uppercase text-fg">Orama</span>
        <div className="w-full max-w-[280px]">
          <OramaCloud />
        </div>
        <figcaption className="text-center">
          <p className="font-display font-semibold text-fg text-lg">Built for many owners. No switch.</p>
          <p className="text-sm text-muted">Built for machines run by independent people, working as one cloud.</p>
        </figcaption>
      </figure>
    </div>
  );
}
