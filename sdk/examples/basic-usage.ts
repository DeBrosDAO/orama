/**
 * Basic Usage Example
 *
 * This example demonstrates the fundamental usage of the DeBros Network SDK.
 * It covers client initialization, database operations, pub/sub, and caching.
 */

import { createClient } from '../src/index';

async function main() {
  // 1. Create client
  const client = createClient({
    baseURL: 'http://localhost:6001',
    apiKey: 'ak_your_key:default',
    debug: true, // Enable debug logging
  });

  console.log('✓ Client created');

  // 2. Database operations
  console.log('\n--- Database Operations ---');

  // Create table
  await client.db.createTable(
    `CREATE TABLE IF NOT EXISTS users (
      id INTEGER PRIMARY KEY AUTOINCREMENT,
      name TEXT NOT NULL,
      email TEXT UNIQUE NOT NULL,
      created_at INTEGER DEFAULT (strftime('%s', 'now'))
    )`
  );
  console.log('✓ Table created');

  // Insert data
  const result = await client.db.exec(
    'INSERT INTO users (name, email) VALUES (?, ?)',
    ['Alice Johnson', 'alice@example.com']
  );
  console.log(`✓ Inserted user with ID: ${result.last_insert_id}`);

  // Query data
  const users = await client.db.query(
    'SELECT * FROM users WHERE email = ?',
    ['alice@example.com']
  );
  console.log('✓ Found users:', users);

  // 3. Pub/Sub messaging
  console.log('\n--- Pub/Sub Messaging ---');

  const subscription = await client.pubsub.subscribe('demo-topic', {
    onMessage: (msg) => {
      console.log(`✓ Received message: "${msg.data}" at ${new Date(msg.timestamp).toISOString()}`);
    },
    onError: (err) => console.error('Subscription error:', err),
  });
  console.log('✓ Subscribed to demo-topic');

  // Publish a message
  await client.pubsub.publish('demo-topic', 'Hello from the SDK!');
  console.log('✓ Published message');

  // Wait a bit for message delivery
  await new Promise(resolve => setTimeout(resolve, 1000));

  // Close subscription
  subscription.close();
  console.log('✓ Subscription closed');

  // 4. Cache operations
  console.log('\n--- Cache Operations ---');

  // Put value with 1-hour TTL
  await client.cache.put('default', 'user:alice', {
    id: result.last_insert_id,
    name: 'Alice Johnson',
    email: 'alice@example.com',
  }, '1h');
  console.log('✓ Cached user data');

  // Get value
  const cached = await client.cache.get('default', 'user:alice');
  if (cached) {
    console.log('✓ Retrieved from cache:', cached.value);
  }

  // 5. Network health check
  console.log('\n--- Network Operations ---');

  const healthy = await client.network.health();
  console.log(`✓ Gateway health: ${healthy ? 'OK' : 'FAIL'}`);

  const status = await client.network.status();
  console.log(`✓ Network status: ${status.peer_count} peers connected`);

  console.log('\n--- Example completed successfully ---');
}

// Run example
main().catch(console.error);
