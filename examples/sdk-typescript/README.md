# DeBros Gateway TypeScript SDK (Minimal Example)

Minimal, dependency-light wrapper around the HTTP Gateway.

Usage:

```bash
npm i
export GATEWAY_BASE_URL=http://127.0.0.1:6001
export GATEWAY_API_KEY=your_api_key
```

```ts
import { GatewayClient } from './src/client';

const c = new GatewayClient(process.env.GATEWAY_BASE_URL!, process.env.GATEWAY_API_KEY!);
await c.createTable('CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)');
await c.transaction([
  'INSERT INTO users (id,name) VALUES (1,\'Alice\')'
]);
const res = await c.query('SELECT name FROM users WHERE id = ?', [1]);
console.log(res.rows);
```
