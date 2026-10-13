import { afterEach, describe, expect, it, vi } from "vitest";
import { CHAIN_ID, FakeStream, HEAD, HEAD_TIME } from "./fake-gateway";
import { browserEventSource, createHeadFeed, readHeadEvent } from "./head-feed";

const URL = "https://gw.example/v1/chain/head/stream";

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe("readHeadEvent", () => {
  it("TestReadHeadEvent_reads_the_gateways_head", () => {
    expect(readHeadEvent(JSON.stringify({ height: 7, time: HEAD_TIME, chain_id: CHAIN_ID }))).toEqual({
      height: 7,
      time: "2026-10-08T12:00:00.000Z",
      chainId: CHAIN_ID,
    });
  });

  it.each([
    ["not a string", 5],
    ["not JSON", "{"],
    ["not an object", "[1]"],
    ["no height", JSON.stringify({ time: HEAD_TIME, chain_id: "x" })],
    ["a negative height", JSON.stringify({ height: -1, time: HEAD_TIME, chain_id: "x" })],
    ["a fractional height", JSON.stringify({ height: 1.5, time: HEAD_TIME, chain_id: "x" })],
    ["a bad time", JSON.stringify({ height: 1, time: "yesterday", chain_id: "x" })],
    ["a chain id that is too long", JSON.stringify({ height: 1, time: HEAD_TIME, chain_id: "x".repeat(65) })],
    ["no chain id", JSON.stringify({ height: 1, time: HEAD_TIME })],
  ])("TestReadHeadEvent_refuses_%s", (_name, data) => {
    expect(() => readHeadEvent(data)).toThrow(/malformed/);
  });
});

describe("browserEventSource", () => {
  it("TestBrowserEventSource_is_null_where_there_is_no_event_source", () => {
    vi.stubGlobal("EventSource", undefined);
    expect(browserEventSource()).toBeNull();
  });

  it("TestBrowserEventSource_opens_the_url_it_is_given", () => {
    const opened: string[] = [];
    vi.stubGlobal(
      "EventSource",
      class {
        constructor(url: string) {
          opened.push(url);
        }
      },
    );
    browserEventSource()?.(URL);
    expect(opened).toEqual([URL]);
  });
});

describe("createHeadFeed", () => {
  it("TestHeadFeed_current_opens_the_stream_takes_the_first_head_and_closes_it", async () => {
    const stream = new FakeStream();
    const feed = createHeadFeed(URL, stream.factory);
    expect(stream.opened).toEqual([]);
    expect(await feed.current()).toEqual({ height: HEAD, time: "2026-10-08T12:00:00.000Z", chainId: CHAIN_ID });
    expect(stream.opened).toEqual([URL]);
    expect(stream.open).toBe(0);
  });

  it("TestHeadFeed_concurrent_current_calls_share_one_stream", async () => {
    const stream = new FakeStream();
    const feed = createHeadFeed(URL, stream.factory);
    const [a, b] = await Promise.all([feed.current(), feed.current()]);
    expect(a).toEqual(b);
    expect(stream.opened).toHaveLength(1);
  });

  it("TestHeadFeed_current_with_a_subscriber_answers_with_the_newest_head_and_keeps_the_stream", async () => {
    const stream = new FakeStream();
    const feed = createHeadFeed(URL, stream.factory);
    const stop = feed.subscribe(() => undefined, () => undefined);
    await feed.current();
    stream.push(HEAD + 5);
    expect((await feed.current()).height).toBe(HEAD + 5);
    expect(stream.open).toBe(1);
    stop();
    expect(stream.open).toBe(0);
  });

  it("TestHeadFeed_the_first_head_is_not_announced_to_a_subscriber_that_had_none", async () => {
    const stream = new FakeStream();
    const feed = createHeadFeed(URL, stream.factory);
    const heads: number[] = [];
    feed.subscribe((h) => heads.push(h.height), () => undefined);
    await feed.current();
    expect(heads).toEqual([]);
    stream.push(HEAD + 1);
    expect(heads).toEqual([HEAD + 1]);
  });

  it("TestHeadFeed_a_subscriber_that_joins_late_hears_only_what_comes_after_the_head_it_found", async () => {
    const stream = new FakeStream();
    const feed = createHeadFeed(URL, stream.factory);
    feed.subscribe(() => undefined, () => undefined);
    await feed.current();
    stream.push(HEAD + 3);
    const late: number[] = [];
    feed.subscribe((h) => late.push(h.height), () => undefined);
    stream.push(HEAD + 3);
    stream.push(HEAD + 4);
    expect(late).toEqual([HEAD + 4]);
  });

  it("TestHeadFeed_unavailable_before_any_head_rejects_current_and_closes_the_stream", async () => {
    const listeners = new Map<string, (event: { data?: unknown }) => void>();
    const down = { onerror: null, readyState: 1, addEventListener: (type: string, l: (event: { data?: unknown }) => void) => listeners.set(type, l), close: vi.fn() };
    const feed = createHeadFeed(URL, () => down);
    const pending = feed.current();
    listeners.get("unavailable")?.({});
    await expect(pending).rejects.toThrow("The chain could not be reached.");
    expect(down.close).toHaveBeenCalledTimes(1);
  });

  it("TestHeadFeed_a_stream_that_never_speaks_is_a_timeout_not_a_hang", async () => {
    vi.useFakeTimers();
    const silent = { onerror: null, readyState: 1, addEventListener: () => undefined, close: vi.fn() };
    const feed = createHeadFeed(URL, () => silent);
    const pending = feed.current();
    const assertion = expect(pending).rejects.toThrow("The chain could not be reached.");
    await vi.advanceTimersByTimeAsync(15_001);
    await assertion;
    expect(silent.close).toHaveBeenCalledTimes(1);
  });

  it("TestHeadFeed_without_an_event_source_current_and_subscribe_both_say_so", async () => {
    const feed = createHeadFeed(URL, null);
    await expect(feed.current()).rejects.toThrow("This browser cannot follow the chain live.");
    const errors: string[] = [];
    const stop = feed.subscribe(() => undefined, (e) => errors.push(e.message));
    expect(errors).toEqual(["This browser cannot follow the chain live."]);
    stop();
  });

  it("TestHeadFeed_stop_is_idempotent_and_reopens_on_the_next_subscriber", async () => {
    const stream = new FakeStream();
    const feed = createHeadFeed(URL, stream.factory);
    const stop = feed.subscribe(() => undefined, () => undefined);
    stop();
    stop();
    expect(stream.open).toBe(0);
    const again = feed.subscribe(() => undefined, () => undefined);
    expect(stream.opened).toHaveLength(2);
    again();
    expect(stream.open).toBe(0);
  });

  it("TestHeadFeed_a_dropped_connection_is_reported_and_the_next_head_goes_on", async () => {
    const stream = new FakeStream();
    const feed = createHeadFeed(URL, stream.factory);
    const heads: number[] = [];
    const errors: string[] = [];
    feed.subscribe((h) => heads.push(h.height), (e) => errors.push(e.message));
    await feed.current();
    stream.drop();
    stream.push(HEAD + 1);
    expect(errors).toEqual(["The live connection to the chain was lost; it reconnects by itself."]);
    expect(heads).toEqual([HEAD + 1]);
  });

  it("TestHeadFeed_a_gateway_never_reached_is_unreachable_not_a_lost_connection", () => {
    // A TLS error or a dead gateway: the browser retries on its own, and nothing was ever connected to lose.
    const listeners = new Map<string, (event: { data?: unknown }) => void>();
    const never = { onerror: null as ((e: Event) => unknown) | null, readyState: 0, addEventListener: (t: string, l: (event: { data?: unknown }) => void) => listeners.set(t, l), close: vi.fn() };
    const feed = createHeadFeed(URL, () => never);
    const errors: string[] = [];
    feed.subscribe(() => undefined, (e) => errors.push(e.message));
    never.onerror?.(new Event("error"));
    expect(errors).toHaveLength(1);
    expect(errors[0]).toContain("The chain could not be reached. This browser could not read the gateway at gw.example.");
    expect(errors[0]).not.toContain("lost");
  });

  describe("a stream the gateway refuses", () => {
    it("TestHeadFeed_a_refused_stream_is_reported_and_opened_again_later", async () => {
      vi.useFakeTimers();
      const stream = new FakeStream();
      const feed = createHeadFeed(URL, stream.factory);
      const heads: number[] = [];
      const errors: string[] = [];
      feed.subscribe((h) => heads.push(h.height), (e) => errors.push(e.message));
      await vi.advanceTimersByTimeAsync(0);

      stream.refuse();
      expect(errors).toEqual([
        "The gateway at gw.example refused the live connection: it may have too many open, or it may not allow reads from this site (CORS). Trying again shortly.",
      ]);
      expect(stream.open).toBe(0);

      await vi.advanceTimersByTimeAsync(4_999);
      expect(stream.opened).toHaveLength(1);
      await vi.advanceTimersByTimeAsync(2);
      expect(stream.opened).toHaveLength(2);
      stream.push(HEAD + 1);
      expect(heads).toEqual([HEAD + 1]);
    });

    it("TestHeadFeed_each_refusal_waits_twice_as_long_and_a_head_starts_the_count_again", async () => {
      vi.useFakeTimers();
      const stream = new FakeStream();
      stream.withholdHead = true;
      const feed = createHeadFeed(URL, stream.factory);
      feed.subscribe(() => undefined, () => undefined);
      await vi.advanceTimersByTimeAsync(0);

      stream.refuse();
      await vi.advanceTimersByTimeAsync(5_000);
      expect(stream.opened).toHaveLength(2);
      stream.refuse();
      await vi.advanceTimersByTimeAsync(9_999);
      expect(stream.opened).toHaveLength(2);
      await vi.advanceTimersByTimeAsync(2);
      expect(stream.opened).toHaveLength(3);

      stream.push(HEAD + 1);
      stream.refuse();
      await vi.advanceTimersByTimeAsync(5_001);
      expect(stream.opened).toHaveLength(4);
    });

    it("TestHeadFeed_the_wait_has_a_ceiling", async () => {
      vi.useFakeTimers();
      const stream = new FakeStream();
      stream.withholdHead = true;
      const feed = createHeadFeed(URL, stream.factory);
      feed.subscribe(() => undefined, () => undefined);
      await vi.advanceTimersByTimeAsync(0);
      for (let i = 0; i < 8; i++) {
        stream.refuse();
        await vi.advanceTimersByTimeAsync(60_001);
      }
      const before = stream.opened.length;
      stream.refuse();
      await vi.advanceTimersByTimeAsync(59_999);
      expect(stream.opened).toHaveLength(before);
      await vi.advanceTimersByTimeAsync(2);
      expect(stream.opened).toHaveLength(before + 1);
    });

    it("TestHeadFeed_a_reader_that_arrives_during_the_wait_does_not_skip_it", async () => {
      vi.useFakeTimers();
      const stream = new FakeStream();
      stream.withholdHead = true;
      const feed = createHeadFeed(URL, stream.factory);
      feed.subscribe(() => undefined, () => undefined);
      await vi.advanceTimersByTimeAsync(0);
      stream.refuse();

      const late: number[] = [];
      feed.subscribe((h) => late.push(h.height), () => undefined);
      expect(stream.opened).toHaveLength(1);
      await vi.advanceTimersByTimeAsync(5_001);
      expect(stream.opened).toHaveLength(2);
      stream.push(HEAD + 1);
      stream.push(HEAD + 2);
      expect(late).toEqual([HEAD + 2]);
    });

    it("TestHeadFeed_the_last_reader_leaving_cancels_the_reopen", async () => {
      vi.useFakeTimers();
      const stream = new FakeStream();
      stream.withholdHead = true;
      const feed = createHeadFeed(URL, stream.factory);
      const stop = feed.subscribe(() => undefined, () => undefined);
      await vi.advanceTimersByTimeAsync(0);
      stream.refuse();
      stop();
      await vi.advanceTimersByTimeAsync(120_000);
      expect(stream.opened).toHaveLength(1);
    });

    it("TestHeadFeed_current_on_a_refused_stream_rejects_and_does_not_reopen", async () => {
      vi.useFakeTimers();
      const listeners = new Map<string, (event: { data?: unknown }) => void>();
      const refused = { onerror: null as ((e: Event) => unknown) | null, readyState: 1, addEventListener: (t: string, l: (event: { data?: unknown }) => void) => listeners.set(t, l), close: vi.fn() };
      const feed = createHeadFeed(URL, () => refused);
      const pending = feed.current();
      const assertion = expect(pending).rejects.toThrow("The gateway at gw.example refused the live connection");
      refused.readyState = 2;
      refused.onerror?.(new Event("error"));
      await assertion;
      await vi.advanceTimersByTimeAsync(120_000);
      expect(refused.close).toHaveBeenCalledTimes(1);
    });
  });
});
