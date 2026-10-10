import { parseNorama } from "../../model/units";
import type { Counterparty } from "../../model/types";

export const MAP_MAX_NODES = 5;
export const MAP_CENTER_RADIUS = 22;
export const MAP_NODE_RADIUS = 17;
export const MAP_MIN_STROKE = 1;
export const MAP_MAX_STROKE = 6;
/** Room under a node for its name. */
const LABEL_SPACE = 22;
const EDGE_MARGIN = 6;
const FULL_TURN = Math.PI * 2;
const SCALE = 1000n;

export interface MapSize {
  width: number;
  height: number;
}

export interface MapNode {
  counterparty: Counterparty;
  x: number;
  y: number;
  /** Edge thickness from the centre to this node. */
  strokeWidth: number;
}

export interface MoneyMapLayout {
  center: { x: number; y: number };
  nodes: MapNode[];
}

function strokeFor(volume: bigint, max: bigint): number {
  if (max === 0n) return MAP_MIN_STROKE;
  const share = Number((volume * SCALE) / max) / Number(SCALE);
  return MAP_MIN_STROKE + share * (MAP_MAX_STROKE - MAP_MIN_STROKE);
}

/**
 * Place the wallet in the centre and up to five counterparties around it at
 * even angles, the first straight up. Edge thickness follows volume relative
 * to the busiest counterparty; if nobody moved anything every edge is thin.
 */
export function layoutMoneyMap(counterparties: Counterparty[], size: MapSize): MoneyMapLayout {
  const shown = counterparties.slice(0, MAP_MAX_NODES);
  const cx = size.width / 2;
  const cy = (size.height - LABEL_SPACE) / 2;
  const rx = cx - MAP_NODE_RADIUS - EDGE_MARGIN;
  const ry = cy - MAP_NODE_RADIUS - EDGE_MARGIN;
  const volumes = shown.map((c) => parseNorama(c.volume));
  const max = volumes.reduce((a, b) => (b > a ? b : a), 0n);
  const nodes = shown.map((counterparty, i) => {
    const angle = -Math.PI / 2 + (i / shown.length) * FULL_TURN;
    return {
      counterparty,
      x: cx + rx * Math.cos(angle),
      y: cy + ry * Math.sin(angle),
      strokeWidth: strokeFor(volumes[i] as bigint, max),
    };
  });
  return { center: { x: cx, y: cy }, nodes };
}
