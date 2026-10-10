import { describe, it, expect, vi } from "vitest";
import { HttpClient } from "../../../src/core/http";
import { SDKError } from "../../../src/errors";

/**
 * Bugboard #144 — HttpClient.uploadFile must accept an external AbortSignal and
 * honor it for the in-flight request, distinguishably from the internal timeout:
 *  (1) a caller abort terminates the in-flight fetch (socket-level),
 *  (2) a caller abort rejects with code "ABORTED" (never "TIMEOUT") and is
 *      NEVER retried,
 *  (3) the internal timeout still surfaces as "TIMEOUT".
 */
describe("Bug #144 — uploadFile honors an external AbortSignal", () => {
  // A fetch that stays in-flight until its signal aborts, then rejects with a
  // DOM-style AbortError — mimicking a real cancelled upload.
  function abortableFetch() {
    return vi.fn((_url: any, opts: any) => {
      return new Promise((_resolve, reject) => {
        const signal: AbortSignal = opts.signal;
        const fail = () => {
          const e = new Error("The operation was aborted");
          (e as any).name = "AbortError";
          reject(e);
        };
        if (signal.aborted) return fail();
        signal.addEventListener("abort", fail, { once: true });
        // otherwise never resolves on its own (in-flight)
      });
    });
  }

  function client(fetchImpl: any, maxRetries = 0) {
    return new HttpClient({
      baseURL: "https://gw.example",
      maxRetries,
      timeout: 5000,
      fetch: fetchImpl,
    });
  }

  it("caller abort mid-flight → rejects code ABORTED, fetch called exactly once (no retry)", async () => {
    const fetchImpl = abortableFetch();
    const ac = new AbortController();
    const c = client(fetchImpl, 3); // retries enabled — must NOT retry a caller abort
    const p = c.uploadFile("/v1/storage/upload", new FormData(), {
      signal: ac.signal,
    });
    ac.abort();
    const err = await p.catch((e) => e);
    expect(err).toBeInstanceOf(SDKError);
    expect(err.code).toBe("ABORTED");
    expect(fetchImpl).toHaveBeenCalledTimes(1);
  });

  it("signal already aborted before start → rejects ABORTED, fetch never called", async () => {
    const fetchImpl = vi.fn();
    const ac = new AbortController();
    ac.abort();
    const err = await client(fetchImpl)
      .uploadFile("/v1/storage/upload", new FormData(), { signal: ac.signal })
      .catch((e) => e);
    expect(err).toBeInstanceOf(SDKError);
    expect(err.code).toBe("ABORTED");
    expect(fetchImpl).not.toHaveBeenCalled();
  });

  it("internal timeout still surfaces as TIMEOUT (not ABORTED)", async () => {
    const fetchImpl = abortableFetch();
    const err = await client(fetchImpl)
      .uploadFile("/v1/storage/upload", new FormData(), { timeout: 10 })
      .catch((e) => e);
    expect(err).toBeInstanceOf(SDKError);
    expect(err.code).toBe("TIMEOUT");
  });

  it("no signal / not aborted → normal success", async () => {
    const okFetch = vi.fn(
      async () =>
        new Response(JSON.stringify({ ok: true, cid: "Qm1" }), {
          status: 200,
          headers: { "content-type": "application/json" },
        })
    );
    const res = await client(okFetch).uploadFile(
      "/v1/storage/upload",
      new FormData(),
      { signal: new AbortController().signal }
    );
    expect(res).toEqual({ ok: true, cid: "Qm1" });
  });
});
