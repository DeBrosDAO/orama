const http = require('http');

const GO_API_URL = process.env.GO_API_URL || 'http://localhost:8080';
const PORT = process.env.PORT || 3000;

async function fetchJSON(url, options = {}) {
  const resp = await fetch(url, options);
  return resp.json();
}

const server = http.createServer(async (req, res) => {
  // CORS is handled by the gateway — don't set headers here to avoid duplicates
  res.setHeader('Content-Type', 'application/json');

  if (req.url === '/health') {
    res.end(JSON.stringify({ status: 'ok', service: 'node-api', go_api: GO_API_URL }));
    return;
  }

  if (req.url === '/api/notes' && req.method === 'GET') {
    try {
      const notes = await fetchJSON(`${GO_API_URL}/api/notes`);
      res.end(JSON.stringify({
        notes,
        fetched_at: new Date().toISOString(),
        source: 'nodejs-proxy',
        go_api: GO_API_URL,
      }));
    } catch (err) {
      res.writeHead(502);
      res.end(JSON.stringify({ error: 'Failed to reach Go API', details: err.message }));
    }
    return;
  }

  if (req.url === '/api/notes' && req.method === 'POST') {
    let body = '';
    req.on('data', chunk => body += chunk);
    req.on('end', async () => {
      try {
        const result = await fetchJSON(`${GO_API_URL}/api/notes`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body,
        });
        res.writeHead(201);
        res.end(JSON.stringify(result));
      } catch (err) {
        res.writeHead(502);
        res.end(JSON.stringify({ error: 'Failed to reach Go API', details: err.message }));
      }
    });
    return;
  }

  res.writeHead(404);
  res.end(JSON.stringify({ error: 'not found' }));
});

server.listen(PORT, () => {
  console.log(`Node API listening on :${PORT}, proxying to ${GO_API_URL}`);
});
