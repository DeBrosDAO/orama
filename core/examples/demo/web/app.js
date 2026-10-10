// The demo page: plain JavaScript, no build step. It calls three functions of a
// namespace through the gateway's invoke route, POST /v1/invoke/<namespace>/<name>
// (docs: Functions, Invocation), and shows what they answer. The functions are
// public, so the calls carry no credential.
"use strict";

const STORAGE_KEY = "orama-demo";
const NAMESPACE_PATTERN = /^[a-z0-9][a-z0-9-]*$/;

// normalizeGateway returns the gateway's origin, or throws why it cannot be one.
// It must be https; http is allowed for a gateway on this machine.
function normalizeGateway(text) {
  let url;
  try {
    url = new URL(String(text).trim());
  } catch {
    throw new Error("the gateway is not a URL like https://ns-myapp.example.network");
  }
  const local = ["localhost", "127.0.0.1", "[::1]"].includes(url.hostname);
  if (url.protocol !== "https:" && !(url.protocol === "http:" && local)) {
    throw new Error("the gateway must be https");
  }
  return url.origin;
}

// invokeURL is where a function of the namespace is invoked.
function invokeURL(config, name) {
  if (!NAMESPACE_PATTERN.test(config.namespace || "")) {
    throw new Error("the namespace is a name like myapp");
  }
  return normalizeGateway(config.gateway) + "/v1/invoke/" + config.namespace + "/" + encodeURIComponent(name);
}

// readConfig is the page's configuration: the URL's query, then config.js, then
// what an earlier visit stored. Anything missing is "".
function readConfig(search, preset, stored) {
  const q = new URLSearchParams(search);
  const pick = (key) => q.get(key) || (preset && preset[key]) || (stored && stored[key]) || "";
  return { gateway: pick("gateway"), namespace: pick("namespace") };
}

async function invoke(config, name, payload) {
  const res = await fetch(invokeURL(config, name), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  });
  const text = await res.text();
  let body;
  try {
    body = JSON.parse(text);
  } catch {
    throw new Error("HTTP " + res.status + ": " + text.slice(0, 200));
  }
  if (!res.ok) {
    throw new Error("HTTP " + res.status + ": " + (body.error && body.error.message || body.error || text.slice(0, 200)));
  }
  if (body && typeof body.error === "string") {
    throw new Error(body.error);
  }
  return body;
}

if (typeof document !== "undefined") {
  const $ = (id) => document.getElementById(id);
  let config = null;

  const stored = () => {
    try { return JSON.parse(localStorage.getItem(STORAGE_KEY) || "null"); } catch { return null; }
  };
  const remember = (c) => {
    try { localStorage.setItem(STORAGE_KEY, JSON.stringify(c)); } catch { /* private mode: ask again next time */ }
  };
  const forget = () => {
    try { localStorage.removeItem(STORAGE_KEY); } catch { /* nothing stored */ }
  };

  // guarded runs an action and shows its failure in the element errId.
  async function guarded(errId, action) {
    $(errId).textContent = "";
    try {
      await action();
    } catch (err) {
      $(errId).textContent = err.message;
    }
  }

  async function showVisits(action) {
    const out = await invoke(config, "visits", { page: "home", action });
    $("visits-count").textContent = String(out.visits);
  }

  async function showGuestbook(payload) {
    const out = await invoke(config, "guestbook", payload);
    const list = $("gb-entries");
    list.replaceChildren(...out.entries.map((e) => {
      const li = document.createElement("li");
      const who = document.createElement("strong");
      who.textContent = e.name;
      const when = document.createElement("small");
      when.textContent = " " + e.at;
      const msg = document.createElement("div");
      msg.textContent = e.message;
      li.append(who, when, msg);
      return li;
    }));
  }

  function start() {
    $("setup").hidden = true;
    guarded("visits-error", () => showVisits("hit"));
    guarded("gb-error", () => showGuestbook({ action: "list" }));
  }

  function setup(c) {
    config = c;
    try {
      invokeURL(c, "hello");
    } catch {
      $("gateway").value = c.gateway;
      $("namespace").value = c.namespace;
      $("setup").hidden = false;
      return;
    }
    start();
  }

  $("setup-form").addEventListener("submit", (ev) => {
    ev.preventDefault();
    $("setup-error").textContent = "";
    const c = { gateway: $("gateway").value, namespace: $("namespace").value.trim() };
    try {
      invokeURL(c, "hello");
    } catch (err) {
      $("setup-error").textContent = err.message;
      return;
    }
    remember(c);
    setup(c);
  });

  $("hello-form").addEventListener("submit", (ev) => {
    ev.preventDefault();
    guarded("hello-out", async () => {
      const out = await invoke(config, "hello", { name: $("hello-name").value });
      $("hello-out").textContent = out.greeting + "\ncaller: " + out.caller;
    });
  });

  $("visits-peek").addEventListener("click", () => guarded("visits-error", () => showVisits("peek")));

  $("guestbook-form").addEventListener("submit", (ev) => {
    ev.preventDefault();
    const button = ev.currentTarget.querySelector("button");
    button.disabled = true;
    guarded("gb-error", async () => {
      await showGuestbook({ action: "sign", name: $("gb-name").value, message: $("gb-message").value });
      $("gb-message").value = "";
    }).finally(() => { button.disabled = false; });
  });

  $("reset").addEventListener("click", () => { forget(); setup({ gateway: "", namespace: "" }); });

  setup(readConfig(location.search, window.ORAMA_DEMO, stored()));
}

if (typeof module !== "undefined") {
  module.exports = { normalizeGateway, invokeURL, readConfig };
}
