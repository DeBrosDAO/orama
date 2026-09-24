import {
  Check,
  Database,
  Globe,
  KeyRound,
  Lock,
  Server,
  ShieldCheck,
  Smartphone,
  Wallet,
  X,
  Zap,
} from "lucide-react";
import { Diagram, Edge, Label, NodeDot } from "./diagram";

type Pt = [number, number];

/* ── 1. The mesh: independent machines, encrypted links ─────────────────── */

const MESH: Pt[] = [
  [70, 60], [190, 40], [320, 70], [360, 170], [270, 220], [140, 230], [40, 160], [200, 135],
];
const MESH_LINKS: [number, number][] = [
  [0, 1], [1, 2], [2, 3], [3, 4], [4, 5], [5, 6], [6, 0],
  [7, 0], [7, 1], [7, 2], [7, 3], [7, 4], [7, 5], [7, 6],
];
const LOCKED = new Set([1, 4, 8, 11]);

export function MeshDiagram() {
  return (
    <Diagram viewBox="0 0 400 270" label="Independent machines joined by encrypted links">
      {MESH_LINKS.map(([a, b], i) => (
        <Edge key={i} from={MESH[a]} to={MESH[b]} flowing={i % 2 === 0} />
      ))}
      {MESH_LINKS.map(([a, b], i) => {
        if (!LOCKED.has(i)) return null;
        const mx = (MESH[a][0] + MESH[b][0]) / 2;
        const my = (MESH[a][1] + MESH[b][1]) / 2;
        return (
          <g key={`lock-${i}`}>
            <circle cx={mx} cy={my} r={9} className="fill-bg stroke-accent/40" strokeWidth={1} />
            <Lock x={mx - 5} y={my - 5} width={10} height={10} className="text-fg" strokeWidth={2} />
          </g>
        );
      })}
      {MESH.map(([x, y], i) => (
        <NodeDot key={i} x={x} y={y} icon={Server} tone={i === 7 ? "bright" : "normal"} />
      ))}
      <Label x={200} y={262}>Every link encrypted · WireGuard</Label>
    </Diagram>
  );
}

/* ── 2. Your app gets its own cluster ──────────────────────────────────── */

const GRID: Pt[] = [];
for (let row = 0; row < 3; row++) {
  for (let col = 0; col < 5; col++) GRID.push([50 + col * 75, 45 + row * 80]);
}
// Two apps, each on its own three machines, in separate corners so the
// isolation reads at a glance.
const YOURS = [6, 10, 12];
const THEIRS = [3, 4, 9];

export function ClusterDiagram() {
  const yours = YOURS.map((i) => GRID[i]);
  const theirs = THEIRS.map((i) => GRID[i]);
  return (
    <Diagram viewBox="0 0 400 260" label="Your app runs on its own private cluster of three machines">
      <polygon points={theirs.map((p) => p.join(",")).join(" ")} className="stroke-border fill-white/[0.01]" strokeDasharray="3 4" />
      <polygon points={yours.map((p) => p.join(",")).join(" ")} className="stroke-fg/60 fill-white/[0.04]" strokeWidth={1.25} />
      {yours.map((p, i) => (
        <Edge key={i} from={p} to={yours[(i + 1) % 3]} flowing tone="bright" />
      ))}
      {GRID.map(([x, y], i) => {
        const mine = YOURS.includes(i);
        const other = THEIRS.includes(i);
        return (
          <NodeDot
            key={i}
            x={x}
            y={y}
            r={mine ? 18 : 13}
            icon={mine ? Database : other ? Zap : Server}
            tone={mine ? "bright" : other ? "normal" : "dim"}
          />
        );
      })}
      <Label x={GRID[11][0]} y={GRID[11][1] + 42} tone="bright">Your app</Label>
      <Label x={(GRID[3][0] + GRID[4][0]) / 2} y={GRID[3][1] - 24}>Another app</Label>
    </Diagram>
  );
}

/* ── 3. A request finds its way ────────────────────────────────────────── */

export function RequestFlowDiagram() {
  const phone: Pt = [40, 130];
  const dns: Pt = [130, 130];
  const edge: Pt = [225, 130];
  const cluster: Pt[] = [[330, 70], [370, 150], [300, 200]];
  return (
    <Diagram viewBox="0 0 400 260" label="A visitor reaches your app through any machine in the network">
      <Edge from={phone} to={dns} flowing tone="bright" />
      <Edge from={dns} to={edge} flowing tone="bright" />
      {cluster.map((p, i) => (
        <Edge key={i} from={edge} to={p} flowing={i === 0} tone={i === 0 ? "bright" : "normal"} />
      ))}
      {cluster.map((p, i) => (
        <Edge key={`c${i}`} from={p} to={cluster[(i + 1) % 3]} />
      ))}
      <NodeDot x={phone[0]} y={phone[1]} r={20} icon={Smartphone} tone="bright" />
      <NodeDot x={dns[0]} y={dns[1]} r={18} icon={Globe} />
      <NodeDot x={edge[0]} y={edge[1]} r={18} icon={Server} />
      {cluster.map(([x, y], i) => (
        <NodeDot key={`n${i}`} x={x} y={y} r={16} icon={Database} tone="bright" />
      ))}
      <Label x={phone[0]} y={172}>Visitor</Label>
      <Label x={dns[0]} y={170}>Name</Label>
      <Label x={edge[0]} y={170}>Any node</Label>
      <Label x={335} y={240} tone="bright">Your cluster</Label>
    </Diagram>
  );
}

/* ── 4. Log in with a wallet ───────────────────────────────────────────── */

export function WalletLoginDiagram() {
  const steps: { at: Pt; icon: typeof Wallet; label: string }[] = [
    { at: [50, 120], icon: Wallet, label: "Your wallet" },
    { at: [150, 120], icon: KeyRound, label: "Signs" },
    { at: [250, 120], icon: ShieldCheck, label: "Orama checks" },
    { at: [350, 120], icon: Check, label: "You're in" },
  ];
  return (
    <Diagram viewBox="0 0 400 230" label="Sign in by signing a message with your wallet. No email, no password.">
      <g opacity={0.8}>
        <rect x={120} y={22} width={160} height={30} rx={4} className="stroke-border fill-surface" strokeDasharray="3 3" />
        <text x={200} y={42} textAnchor="middle" fontSize={11} className="fill-muted font-mono line-through" letterSpacing="0.06em">
          EMAIL + PASSWORD
        </text>
        <X x={272} y={14} width={16} height={16} className="text-muted" strokeWidth={2} />
      </g>
      {steps.slice(0, -1).map((s, i) => (
        <Edge key={i} from={s.at} to={steps[i + 1].at} flowing tone="bright" />
      ))}
      {steps.map((s, i) => (
        <g key={s.label}>
          <NodeDot x={s.at[0]} y={s.at[1]} r={22} icon={s.icon} tone={i === 0 || i === 3 ? "bright" : "normal"} />
          <Label x={s.at[0]} y={s.at[1] + 44} tone={i === 3 ? "bright" : "muted"}>
            {s.label}
          </Label>
        </g>
      ))}
    </Diagram>
  );
}

/* ── Resilience: one machine fails, the app stays up ───────────────────── */

export function ResilienceDiagram() {
  const nodes: Pt[] = [[200, 50], [320, 190], [80, 190]];
  return (
    <Diagram viewBox="0 0 400 260" label="If one machine in your cluster fails, the rest of the cluster keeps your app running">
      <Edge from={nodes[0]} to={nodes[1]} flowing tone="bright" />
      <Edge from={nodes[1]} to={nodes[2]} tone="dim" />
      <Edge from={nodes[2]} to={nodes[0]} tone="dim" />
      <NodeDot x={nodes[0][0]} y={nodes[0][1]} r={22} icon={Database} tone="bright" />
      <NodeDot x={nodes[1][0]} y={nodes[1][1]} r={22} icon={Database} tone="bright" />
      <NodeDot x={nodes[2][0]} y={nodes[2][1]} r={22} icon={Database} tone="dim" />
      <line x1={62} y1={172} x2={98} y2={208} className="stroke-signal" strokeWidth={2} />
      <line x1={98} y1={172} x2={62} y2={208} className="stroke-signal" strokeWidth={2} />
      <circle cx={200} cy={148} r={6} className="fill-fg animate-breathe" />
      <Label x={200} y={172} tone="bright">App online</Label>
      <Label x={80} y={236}>Offline</Label>
    </Diagram>
  );
}
