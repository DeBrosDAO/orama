// The reference Node.js API: a per-user todo service.
//
// Users call it with the session their wallet signed in with; the app asks
// the namespace gateway who that is (/v1/auth/whoami), so it never verifies a
// token itself. Todos are stored by the "store" WASM function on the
// namespace's RQLite, and each user's list is cached in the namespace's cache;
// the app reaches both as itself, with the workload token the platform hands
// it and that it renews (docs/DEPLOYMENT_GUIDE.md "Your app's own credential").
// No dependencies: it runs with plain `node index.js`.
'use strict';

const fs = require('fs');
const http = require('http');
const https = require('https');
const path = require('path');

// ca.pem is the cluster's trust, shipped with the app by whoever deploys it
// (a test fleet's certificates chain to Let's Encrypt staging).
const ca = fs.readFileSync(path.join(__dirname, 'ca.pem'));
const base = new URL(process.env.ORAMA_GATEWAY_URL);
const storeFn = process.env.STORE_FN || 'store';
const listMap = 'ref-todos';
let token = fs.readFileSync(process.env.ORAMA_TOKEN_FILE, 'utf8').trim();
let renewals = 0;

// call sends a JSON request to the gateway and resolves {status, body}.
function call(method, route, bearer, payload) {
  const body = payload === undefined ? undefined : JSON.stringify(payload);
  const headers = {Authorization: 'Bearer ' + bearer};
  if (body !== undefined) {
    headers['Content-Type'] = 'application/json';
    headers['Content-Length'] = Buffer.byteLength(body);
  }
  return new Promise((resolve, reject) => {
    const req = https.request({hostname: base.hostname, port: base.port || 443, path: route, method, headers, ca, timeout: 30000}, (res) => {
      const chunks = [];
      res.on('data', (c) => chunks.push(c));
      res.on('end', () => {
        const text = Buffer.concat(chunks).toString('utf8');
        let parsed = text;
        try { parsed = JSON.parse(text); } catch (_) { /* not JSON: keep the text */ }
        resolve({status: res.statusCode, body: parsed});
      });
    });
    req.on('timeout', () => req.destroy(new Error('gateway timeout')));
    req.on('error', reject);
    if (body !== undefined) req.write(body);
    req.end();
  });
}

async function renew() {
  const r = await call('POST', '/v1/auth/renew', token);
  if (r.status !== 200 || !r.body.access_token) throw Object.assign(new Error('renew failed'), {status: r.status, body: r.body});
  const changed = r.body.access_token !== token;
  token = r.body.access_token;
  renewals++;
  // Prove the renewed token is accepted before reporting success.
  const who = await call('GET', '/v1/auth/whoami', token);
  return {changed, expires_in: r.body.expires_in, renewals, principal: who.body.principal, role: who.body.role, grants: who.body.grants};
}

function scheduleRenew(seconds) {
  setTimeout(() => renew().then((r) => scheduleRenew(Math.max(r.expires_in / 2, 30)), () => scheduleRenew(60)), seconds * 1000);
}

// user resolves the wallet a request's session belongs to, or null.
async function user(req) {
  const auth = req.headers.authorization || '';
  if (!auth.startsWith('Bearer ')) return null;
  const r = await call('GET', '/v1/auth/whoami', auth.slice('Bearer '.length));
  if (r.status !== 200 || !r.body.authenticated || r.body.principal !== 'wallet') return null;
  return r.body.subject;
}

async function invoke(input) {
  const r = await call('POST', '/v1/functions/' + encodeURIComponent(storeFn) + '/invoke', token, input);
  if (r.status !== 200 || r.body.error) throw Object.assign(new Error('store function failed'), {status: r.status === 200 ? 502 : r.status, body: r.body});
  return r.body;
}

async function listTodos(owner) {
  const hit = await call('POST', '/v1/cache/get', token, {dmap: listMap, key: owner});
  if (hit.status === 200 && typeof hit.body.value === 'string') return {items: JSON.parse(hit.body.value), cached: true};
  const out = await invoke({op: 'list', owner});
  const items = (out.result && out.result.rows) || [];
  const put = await call('POST', '/v1/cache/put', token, {dmap: listMap, key: owner, value: JSON.stringify(items), ttl: '5m'});
  if (put.status !== 200) throw Object.assign(new Error('cache put failed'), {status: put.status, body: put.body});
  return {items, cached: false};
}

async function addTodo(owner, text) {
  const out = await invoke({op: 'put', owner, text});
  await call('POST', '/v1/cache/delete', token, {dmap: listMap, key: owner});
  return {id: out.result && out.result.last_insert_id, writes: out.writes};
}

function readBody(req) {
  return new Promise((resolve, reject) => {
    const chunks = [];
    req.on('data', (c) => chunks.push(c));
    req.on('end', () => resolve(Buffer.concat(chunks).toString('utf8')));
    req.on('error', reject);
  });
}

async function route(req, res) {
  const url = new URL(req.url, 'http://app');
  if (url.pathname === '/health') return [200, {ok: true}];
  if (url.pathname === '/version') return [200, {version: process.env.APP_VERSION || ''}];
  if (url.pathname === '/api/self') {
    const r = await call('GET', '/v1/auth/whoami', token);
    return [r.status, r.body];
  }
  if (url.pathname === '/api/renew' && req.method === 'POST') return [200, await renew()];
  if (url.pathname !== '/api/todos') return [404, {error: 'not found'}];
  const owner = await user(req);
  if (!owner) return [401, {error: 'sign in with your wallet'}];
  if (req.method === 'GET') return [200, Object.assign({owner}, await listTodos(owner))];
  if (req.method !== 'POST') return [405, {error: 'GET or POST'}];
  let text = '';
  try { text = JSON.parse(await readBody(req)).text; } catch (_) { return [400, {error: 'body is not JSON'}]; }
  if (typeof text !== 'string' || text === '' || text.length > 1000) return [400, {error: 'text must be 1-1000 characters'}];
  return [201, await addTodo(owner, text)];
}

http.createServer((req, res) => {
  route(req, res).then(([status, body]) => {
    res.writeHead(status, {'Content-Type': 'application/json'});
    res.end(JSON.stringify(body));
  }, (err) => {
    res.writeHead(err.status || 502, {'Content-Type': 'application/json'});
    res.end(JSON.stringify({error: err.message, gateway: err.body}));
  });
}).listen(Number(process.env.PORT));

scheduleRenew(60);
