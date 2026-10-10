// Run with: node --test test/app.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { createRequire } from "node:module";

const { normalizeGateway, invokeURL, readConfig } = createRequire(import.meta.url)("../web/app.js");

test("normalizeGateway keeps the origin of an https gateway", () => {
  assert.equal(normalizeGateway(" https://ns-myapp.example.network/some/path?x=1 "), "https://ns-myapp.example.network");
});

test("normalizeGateway allows http only on this machine", () => {
  assert.equal(normalizeGateway("http://localhost:6001"), "http://localhost:6001");
  assert.throws(() => normalizeGateway("http://ns-myapp.example.network"), /must be https/);
});

test("normalizeGateway refuses what is not a URL, and other schemes", () => {
  assert.throws(() => normalizeGateway("ns-myapp"), /not a URL/);
  assert.throws(() => normalizeGateway(""), /not a URL/);
  assert.throws(() => normalizeGateway("javascript:alert(1)"), /must be https/);
  assert.throws(() => normalizeGateway("ftp://x.example"), /must be https/);
});

test("invokeURL is the gateway's invoke route for the namespace", () => {
  const config = { gateway: "https://ns-myapp.example.network", namespace: "myapp" };
  assert.equal(invokeURL(config, "visits"), "https://ns-myapp.example.network/v1/invoke/myapp/visits");
});

test("invokeURL refuses a namespace that could change the path", () => {
  for (const namespace of ["", "My App", "a/b", "../x", "A"]) {
    assert.throws(() => invokeURL({ gateway: "https://g.example", namespace }, "hello"), /namespace/, namespace);
  }
});

test("invokeURL escapes the function name", () => {
  const url = invokeURL({ gateway: "https://g.example", namespace: "a" }, "x/y z");
  assert.equal(url, "https://g.example/v1/invoke/a/x%2Fy%20z");
});

test("readConfig prefers the URL, then config.js, then what was stored", () => {
  const preset = { gateway: "https://preset.example", namespace: "preset" };
  const stored = { gateway: "https://stored.example", namespace: "stored" };
  assert.deepEqual(readConfig("?gateway=https://q.example&namespace=q", preset, stored), { gateway: "https://q.example", namespace: "q" });
  assert.deepEqual(readConfig("", preset, stored), preset);
  assert.deepEqual(readConfig("", { gateway: "", namespace: "" }, stored), stored);
  assert.deepEqual(readConfig("", null, null), { gateway: "", namespace: "" });
  assert.deepEqual(readConfig("?namespace=q", { gateway: "https://preset.example" }, null), { gateway: "https://preset.example", namespace: "q" });
});
