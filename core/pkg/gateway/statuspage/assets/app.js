"use strict";

// The public status page. Everything shown comes from /v1/status, which is
// already reduced to what may be public; this script only draws it. Values
// are set with textContent, never parsed as HTML.

const REFRESH_MS = 10000;
const HISTORY_DAYS = 90;
const DAY_MS = 86400000;
const UPTIME_MINOR_BELOW = 99.9;
const UPTIME_MAJOR_BELOW = 95;

const $ = (id) => document.getElementById(id);

function el(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

const STATE_LABEL = {
  operational: "Operational",
  degraded: "Degraded",
  outage: "Outage",
  unknown: "Unknown",
};

function ago(iso) {
  const secs = Math.max(0, Math.round((Date.now() - new Date(iso).getTime()) / 1000));
  if (secs < 60) return secs + "s ago";
  if (secs < 3600) return Math.round(secs / 60) + "m ago";
  return Math.round(secs / 3600) + "h ago";
}

function fmtSeconds(s) {
  if (s === undefined || s === null) return "–";
  if (s < 60) return s.toFixed(s < 10 ? 1 : 0) + "s";
  return Math.round(s / 60) + "m";
}

function fmtNumber(n, digits) {
  return Number(n).toLocaleString(undefined, { maximumFractionDigits: digits });
}

function renderBanner(data) {
  const state = data.overall || "unknown";
  $("banner").className = "banner state-" + state;
  $("headline").textContent = data.headline || STATE_LABEL[state];
  $("updated").textContent = data.updated_at ? "Updated " + ago(data.updated_at) : "No data yet";
}

function renderStats(data) {
  const nodes = data.nodes || { healthy: 0, total: 0 };
  let label = nodes.healthy + " / " + nodes.total;
  if (nodes.unknown) label += " · " + nodes.unknown + " upgrading";
  $("stat-nodes").textContent = label;
  const t = data.traffic;
  $("stat-rps").textContent = t ? fmtNumber(t.rps, 1) : "–";
  $("stat-errors").textContent = t ? fmtNumber(t.error_rate * 100, 2) + "%" : "–";
  $("stat-p95").textContent = t ? fmtNumber(t.p95_ms, 0) + " ms" : "–";
}

function barClass(pct) {
  if (pct === undefined) return "bar";
  if (pct >= UPTIME_MINOR_BELOW) return "bar up";
  if (pct >= UPTIME_MAJOR_BELOW) return "bar minor";
  return "bar major";
}

// dayKeys is the last HISTORY_DAYS UTC dates, oldest first, so a service
// with little history still lines up with the others.
function dayKeys() {
  const keys = [];
  const today = Date.now();
  for (let i = HISTORY_DAYS - 1; i >= 0; i--) {
    keys.push(new Date(today - i * DAY_MS).toISOString().slice(0, 10));
  }
  return keys;
}

function renderBars(history) {
  const byDay = {};
  for (const d of history || []) byDay[d.date] = d.uptime_pct;
  const bars = el("div", "bars");
  for (const day of dayKeys()) {
    const pct = byDay[day];
    const bar = el("div", barClass(pct));
    bar.title = day + ": " + (pct === undefined ? "no data" : fmtNumber(pct, 2) + "% uptime");
    bars.appendChild(bar);
  }
  return bars;
}

function renderComponent(c) {
  const li = el("li", "component");
  const head = el("div", "component-head");
  const left = el("div");
  left.appendChild(el("div", "component-name", c.name));
  left.appendChild(el("div", "component-summary", c.summary));
  head.appendChild(left);
  head.appendChild(el("span", "pill " + c.state, STATE_LABEL[c.state] || c.state));
  li.appendChild(head);
  li.appendChild(renderBars(c.history));
  const foot = el("div", "bars-foot");
  foot.appendChild(el("span", "", HISTORY_DAYS + " days ago"));
  const uptime = c.uptime_pct === null || c.uptime_pct === undefined ? "No history yet" : fmtNumber(c.uptime_pct, 2) + "% uptime";
  foot.appendChild(el("span", "", uptime));
  foot.appendChild(el("span", "", "Today"));
  li.appendChild(foot);
  return li;
}

function renderComponents(data) {
  const list = $("components");
  list.replaceChildren(...(data.components || []).map(renderComponent));
}

function shortAddress(addr) {
  return addr.length > 20 ? addr.slice(0, 10) + "…" + addr.slice(-6) : addr;
}

function renderValidator(v) {
  const li = el("li", "validator");
  const addr = el("span", "validator-addr mono", shortAddress(v.address));
  addr.title = v.address;
  li.appendChild(addr);
  const share = el("div", "share");
  const fill = el("span");
  fill.style.width = Math.min(100, Math.max(0, v.share_pct)) + "%";
  share.appendChild(fill);
  li.appendChild(share);
  li.appendChild(el("span", "validator-pct", fmtNumber(v.share_pct, 1) + "%"));
  return li;
}

function renderChain(data) {
  const chain = data.chain;
  $("chain-card").hidden = !chain;
  if (!chain) return;
  $("chain-id").textContent = chain.chain_id;
  $("chain-height").textContent = fmtNumber(chain.height, 0);
  $("chain-blocktime").textContent = fmtSeconds(chain.avg_block_time_sec);
  $("chain-age").textContent = fmtSeconds(chain.block_age_sec) + " ago";
  $("chain-mempool").textContent = fmtNumber(chain.mempool_txs, 0) + " tx";
  const validators = chain.validators || [];
  $("chain-validator-count").textContent = "(" + validators.length + ")";
  $("validators").replaceChildren(...validators.map(renderValidator));
}

async function refresh() {
  try {
    const res = await fetch("/v1/status", { headers: { Accept: "application/json" }, cache: "no-store" });
    if (!res.ok) throw new Error("HTTP " + res.status);
    const data = await res.json();
    renderBanner(data);
    renderStats(data);
    renderComponents(data);
    renderChain(data);
    $("error").textContent = "";
  } catch (err) {
    $("error").textContent = "Could not load status: " + err.message;
  }
}

refresh();
setInterval(refresh, REFRESH_MS);
