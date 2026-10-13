import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import type { IndexStatus } from "../model/types";
import { IndexLagLine } from "./index-lag";

const status = (over: Partial<IndexStatus> = {}): IndexStatus => ({ startHeight: 1, cursor: 1000, tip: 1000, lag: 0, aggregatesFrom: 1, stuck: null, ...over });

describe("IndexLagLine", () => {
  it("TestIndexLagLine_a_current_index_is_quiet_status_text", () => {
    const out = renderToStaticMarkup(<IndexLagLine index={status()} />);
    expect(out).toContain('role="status"');
    expect(out).toContain('data-index-lag="current"');
    expect(out).toContain("up to date at block 1,000");
    expect(out).not.toContain("text-loss");
  });

  it("TestIndexLagLine_a_lagging_index_is_marked", () => {
    const out = renderToStaticMarkup(<IndexLagLine index={status({ cursor: 970, lag: 30 })} />);
    expect(out).toContain('data-index-lag="behind"');
    expect(out).toContain("30 blocks behind");
  });

  it("TestIndexLagLine_a_stuck_index_is_an_alert", () => {
    const out = renderToStaticMarkup(<IndexLagLine index={status({ lag: 5, stuck: { height: 1001, reason: "boom", statePruned: false, failures: 2 } })} />);
    expect(out).toContain('role="alert"');
    expect(out).toContain("text-loss");
    expect(out).toContain("boom");
  });
});
