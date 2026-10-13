import { describe, expect, it } from "vitest";
import { lagNote } from "./index-lag";
import type { IndexStatus } from "./types";

const status = (over: Partial<IndexStatus> = {}): IndexStatus => ({ startHeight: 1, cursor: 1000, tip: 1000, lag: 0, aggregatesFrom: 1, stuck: null, ...over });

describe("lagNote", () => {
  it("TestLagNote_an_index_that_has_caught_up_says_the_block", () => {
    expect(lagNote(status())).toEqual({ tone: "current", text: "History is up to date at block 1,000." });
  });

  it("TestLagNote_an_index_behind_says_how_far_and_where_both_stand", () => {
    expect(lagNote(status({ cursor: 990, lag: 10 }))).toEqual({
      tone: "behind",
      text: "History is 10 blocks behind the chain: indexed to block 990, the chain is at 1,000.",
    });
  });

  it("TestLagNote_one_block_behind_is_singular", () => {
    expect(lagNote(status({ cursor: 999, lag: 1 })).text).toContain("1 block behind");
  });

  it("TestLagNote_a_stuck_index_says_where_and_why", () => {
    const note = lagNote(status({ cursor: 900, lag: 100, stuck: { height: 901, reason: "disk full", statePruned: false, failures: 4 } }));
    expect(note.tone).toBe("stuck");
    expect(note.text).toBe("The indexer is stuck at block 901: disk full. History stops at block 900, 100 behind the chain.");
  });

  it("TestLagNote_a_pruned_state_is_said_in_plain_words_not_the_nodes", () => {
    const note = lagNote(status({ cursor: 900, lag: 100, stuck: { height: 901, reason: "failed to load state at height 901; version mismatch", statePruned: true, failures: 9 } }));
    expect(note.text).toContain("no longer keeps the chain state it needs");
    expect(note.text).not.toContain("version mismatch");
  });

  it("TestLagNote_a_stuck_index_with_no_reason_still_says_something", () => {
    expect(lagNote(status({ stuck: { height: 5, reason: "", statePruned: false, failures: 1 } })).text).toContain("failed there repeatedly");
  });

  it("TestLagNote_an_index_with_no_block_is_empty_not_up_to_date", () => {
    expect(lagNote(status({ cursor: 0, tip: 50, lag: 50 }))).toEqual({
      tone: "empty",
      text: "The indexer has not indexed a block yet, so the history on this page is empty.",
    });
  });
});
