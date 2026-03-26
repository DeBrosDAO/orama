/**
 * Database CRUD Operations Example
 *
 * Demonstrates comprehensive database operations including:
 * - Table creation and schema management
 * - Insert, Update, Delete operations
 * - QueryBuilder fluent API
 * - Repository pattern (ORM-style)
 * - Transactions
 */

import { createClient } from '../src/index';

interface User {
  id?: number;
  name: string;
  email: string;
  age: number;
  active?: number;
  created_at?: number;
}

async function main() {
  const client = createClient({
    baseURL: 'http://localhost:6001',
    apiKey: 'ak_your_key:default',
  });

  // 1. Create table
  console.log('Creating users table...');
  await client.db.createTable(
    `CREATE TABLE IF NOT EXISTS users (
      id INTEGER PRIMARY KEY AUTOINCREMENT,
      name TEXT NOT NULL,
      email TEXT UNIQUE NOT NULL,
      age INTEGER,
      active INTEGER DEFAULT 1,
      created_at INTEGER DEFAULT (strftime('%s', 'now'))
    )`
  );

  // 2. Raw SQL INSERT
  console.log('\n--- Raw SQL Operations ---');
  const insertResult = await client.db.exec(
    'INSERT INTO users (name, email, age) VALUES (?, ?, ?)',
    ['Bob Smith', 'bob@example.com', 30]
  );
  console.log('Inserted ID:', insertResult.last_insert_id);

  // 3. Raw SQL UPDATE
  await client.db.exec(
    'UPDATE users SET age = ? WHERE id = ?',
    [31, insertResult.last_insert_id]
  );
  console.log('Updated user age');

  // 4. Raw SQL SELECT
  const users = await client.db.query<User>(
    'SELECT * FROM users WHERE email = ?',
    ['bob@example.com']
  );
  console.log('Found users:', users);

  // 5. QueryBuilder
  console.log('\n--- QueryBuilder Operations ---');

  // Insert multiple users for querying
  await client.db.exec("INSERT INTO users (name, email, age) VALUES (?, ?, ?)", ["Charlie", "charlie@example.com", 25]);
  await client.db.exec("INSERT INTO users (name, email, age) VALUES (?, ?, ?)", ["Diana", "diana@example.com", 35]);
  await client.db.exec("INSERT INTO users (name, email, age) VALUES (?, ?, ?)", ["Eve", "eve@example.com", 28]);

  // Complex query with QueryBuilder
  const activeUsers = await client.db
    .createQueryBuilder('users')
    .select('id', 'name', 'email', 'age')
    .where('active = ?', [1])
    .andWhere('age > ?', [25])
    .orderBy('age DESC')
    .limit(10)
    .getMany<User>();

  console.log('Active users over 25:', activeUsers);

  // Get single user
  const singleUser = await client.db
    .createQueryBuilder('users')
    .where('email = ?', ['charlie@example.com'])
    .getOne<User>();

  console.log('Single user:', singleUser);

  // Count users
  const count = await client.db
    .createQueryBuilder('users')
    .where('age > ?', [25])
    .count();

  console.log('Users over 25:', count);

  // 6. Repository Pattern (ORM)
  console.log('\n--- Repository Pattern ---');

  const userRepo = client.db.repository<User>('users');

  // Find all
  const allUsers = await userRepo.find({});
  console.log('All users:', allUsers.length);

  // Find with criteria
  const youngUsers = await userRepo.find({ age: 25 });
  console.log('Users aged 25:', youngUsers);

  // Find one
  const diana = await userRepo.findOne({ email: 'diana@example.com' });
  console.log('Found Diana:', diana);

  // Save (insert new)
  const newUser: User = {
    name: 'Frank',
    email: 'frank@example.com',
    age: 40,
  };
  await userRepo.save(newUser);
  console.log('Saved new user:', newUser);

  // Save (update existing)
  if (diana) {
    diana.age = 36;
    await userRepo.save(diana);
    console.log('Updated Diana:', diana);
  }

  // Remove
  if (newUser.id) {
    await userRepo.remove(newUser);
    console.log('Deleted Frank');
  }

  // 7. Transactions
  console.log('\n--- Transaction Operations ---');

  const txResults = await client.db.transaction([
    {
      kind: 'exec',
      sql: 'INSERT INTO users (name, email, age) VALUES (?, ?, ?)',
      args: ['Grace', 'grace@example.com', 27],
    },
    {
      kind: 'exec',
      sql: 'UPDATE users SET active = ? WHERE age < ?',
      args: [0, 26],
    },
    {
      kind: 'query',
      sql: 'SELECT COUNT(*) as count FROM users WHERE active = ?',
      args: [1],
    },
  ]);

  console.log('Transaction results:', txResults);

  // 8. Get schema
  console.log('\n--- Schema Information ---');
  const schema = await client.db.getSchema();
  console.log('Database schema:', schema);

  console.log('\n--- CRUD operations completed successfully ---');
}

main().catch(console.error);
