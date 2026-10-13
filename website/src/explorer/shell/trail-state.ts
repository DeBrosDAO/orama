import { nextTrail, readSharedTrail } from "../model/trail";

export interface TrailState {
  trail: string[];
  /** True while the trail is the one that arrived in a shared link, not one the reader built by clicking. */
  shared: boolean;
}

export const EMPTY_TRAIL: TrailState = { trail: [], shared: false };

/**
 * A genuine shared link always contains the page it opens on (sharing appends
 * it). A link whose trail omits the landing page was hand-made, so it is
 * dropped instead of being shown as the reader's own path.
 */
export function initialTrailState(search: string, pathname: string): TrailState {
  const trail = readSharedTrail(search);
  return trail.includes(pathname) ? { trail, shared: true } : EMPTY_TRAIL;
}

/** Visiting a page already on the trail keeps it shared; stepping onto a new page makes it the reader's own. */
export function visitPage(state: TrailState, path: string): TrailState {
  const trail = nextTrail(state.trail, path);
  const onTrail = state.trail.includes(path);
  return { trail, shared: state.shared && onTrail && trail.length > 0 };
}

