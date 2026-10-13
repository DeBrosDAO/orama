import { oldestUnarchivedBounds, retainLag, unarchivedRangeCount } from "../../model/storage";
import type { ArchiveHealth, ArchiveRange, RangeState } from "../../model/storage";
import { ChainNotFound, withQuery } from "./client";
import type { ChainClient } from "./client";
import { bool, int, optArr, rec, uint } from "./wire";

const ARCHIVE_PARAMS = "query/orama.archive.v1.Query/Params";
const RETAIN_HEIGHT = "query/orama.archive.v1.Query/RetainHeight";
const RANGE = "query/orama.archive.v1.Query/Range";

/** The oldest unarchived ranges looked up: each is a query of its own. */
const RANGES_SHOWN = 4;

function operatorsOf(raw: unknown, what: string): number {
  return new Set(optArr(raw, what).map(String)).size;
}

/** One range's record: archived, decided on a bundle, or still collecting attestations for one or more. */
export function readRange(raw: unknown): ArchiveRange {
  const r = rec(rec(raw, "archive range").range, "archive range");
  const archived = bool(r.archived, "archive flag");
  const decided = bool(r.decided, "archive decision");
  const candidates = optArr(r.candidates, "archive candidates");
  const best = Math.max(0, ...candidates.map((c) => operatorsOf(rec(c, "archive candidate").operators, "candidate operators")));
  let state: RangeState = "attesting";
  if (archived) state = "archived";
  else if (decided) state = "decided";
  return {
    start: uint(r.start_height, "range start"),
    end: uint(r.end_height, "range end"),
    state,
    attesters: decided || archived ? operatorsOf(r.operators, "range operators") : best,
    replicaDeals: optArr(r.deal_ids, "range deals").length,
  };
}

async function lookUpRange(client: ChainClient, start: number, end: number): Promise<ArchiveRange> {
  const path = withQuery(RANGE, { json: JSON.stringify({ start_height: String(start), end_height: String(end) }) });
  try {
    return readRange(await client.get(path));
  } catch (err) {
    if (err instanceof ChainNotFound) return { start, end, state: "none", attesters: 0, replicaDeals: 0 };
    throw err;
  }
}

/** Where the block archive stands: what is archived, what may be pruned, and what the oldest missing ranges have so far. */
export async function loadArchiveHealth(client: ChainClient): Promise<ArchiveHealth> {
  const [paramsRaw, retainRaw] = await Promise.all([client.get(ARCHIVE_PARAMS), client.get(RETAIN_HEIGHT)]);
  const params = rec(rec(paramsRaw, "archive parameters").params, "archive parameters");
  const retain = rec(retainRaw, "archive retention");
  const rangeBlocks = uint(params.range_blocks, "archive range width");
  const tip = uint(retain.tip, "chain tip");
  const last = uint(retain.last_archived_height, "last archived height");
  const retainHeight = int(retain.retain_height, "retain height");
  const window = uint(retain.retention_window_blocks, "retention window");
  const bounds = oldestUnarchivedBounds(tip, last, rangeBlocks, RANGES_SHOWN);
  return {
    tip,
    lastArchivedHeight: last,
    retainHeight,
    retentionWindowBlocks: window,
    rangeBlocks,
    retainLag: retainLag(tip, retainHeight, window),
    unarchivedRanges: unarchivedRangeCount(tip, last, rangeBlocks),
    oldestUnarchived: await Promise.all(bounds.map((b) => lookUpRange(client, b.start, b.end))),
  };
}
