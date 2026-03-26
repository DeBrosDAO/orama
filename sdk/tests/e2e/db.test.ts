import { describe, it, expect, beforeEach } from "vitest";
import { createTestClient, skipIfNoGateway, generateTableName } from "./setup";

describe("Database", () => {
  if (skipIfNoGateway()) {
    console.log("Skipping database tests");
  }

  let tableName: string;

  beforeEach(() => {
    tableName = generateTableName();
  });

  it("should create a table", async () => {
    const client = await createTestClient();

    await client.db.createTable(
      `CREATE TABLE ${tableName} (id INTEGER PRIMARY KEY, name TEXT, email TEXT)`
    );

    // Verify by querying schema
    const schema = await client.db.getSchema();
    expect(schema).toBeDefined();
  });

  it("should insert and query data", async () => {
    const client = await createTestClient();

    // Create table
    await client.db.createTable(
      `CREATE TABLE ${tableName} (id INTEGER PRIMARY KEY, name TEXT, email TEXT)`
    );

    // Insert data
    const result = await client.db.exec(
      `INSERT INTO ${tableName} (name, email) VALUES (?, ?)`,
      ["Alice", "alice@example.com"]
    );
    expect(result.rows_affected).toBeGreaterThan(0);

    // Query data
    const rows = await client.db.query(
      `SELECT * FROM ${tableName} WHERE email = ?`,
      ["alice@example.com"]
    );
    expect(rows).toHaveLength(1);
    expect(rows[0].name).toBe("Alice");
  });

  it("should use find() and findOne()", async () => {
    const client = await createTestClient();

    // Create table
    await client.db.createTable(
      `CREATE TABLE ${tableName} (id INTEGER PRIMARY KEY, name TEXT, email TEXT)`
    );

    // Insert data
    await client.db.exec(
      `INSERT INTO ${tableName} (name, email) VALUES (?, ?)`,
      ["Bob", "bob@example.com"]
    );

    // Find one
    const bob = await client.db.findOne(tableName, {
      email: "bob@example.com",
    });
    expect(bob).toBeDefined();
    expect(bob?.name).toBe("Bob");

    // Find all
    const all = await client.db.find(tableName, {});
    expect(all.length).toBeGreaterThan(0);
  });

  it("should use QueryBuilder", async () => {
    const client = await createTestClient();

    // Create table
    await client.db.createTable(
      `CREATE TABLE ${tableName} (id INTEGER PRIMARY KEY, name TEXT, email TEXT, active INTEGER)`
    );

    // Insert test data
    await client.db.exec(
      `INSERT INTO ${tableName} (name, email, active) VALUES (?, ?, ?)`,
      ["Charlie", "charlie@example.com", 1]
    );
    await client.db.exec(
      `INSERT INTO ${tableName} (name, email, active) VALUES (?, ?, ?)`,
      ["Diana", "diana@example.com", 0]
    );

    // Query with builder
    const qb = client.db.createQueryBuilder(tableName);
    const active = await qb
      .where("active = ?", [1])
      .orderBy("name")
      .getMany();

    expect(active.length).toBeGreaterThan(0);
    expect(active[0].name).toBe("Charlie");
  });

  it("should use Repository for save/remove", async () => {
    const client = await createTestClient();

    // Create table
    await client.db.createTable(
      `CREATE TABLE ${tableName} (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT, email TEXT)`
    );

    const repo = client.db.repository<{
      id?: number;
      name: string;
      email: string;
    }>(tableName);

    // Save (insert)
    const entity = { name: "Eve", email: "eve@example.com" };
    await repo.save(entity);
    expect(entity.id).toBeDefined();

    // Find one
    const found = await repo.findOne({ email: "eve@example.com" });
    expect(found).toBeDefined();
    expect(found?.name).toBe("Eve");

    // Update
    if (found) {
      found.name = "Eve Updated";
      await repo.save(found);
    }

    // Verify update
    const updated = await repo.findOne({ id: entity.id });
    expect(updated?.name).toBe("Eve Updated");

    // Remove
    if (updated) {
      await repo.remove(updated);
    }

    // Verify deletion
    const deleted = await repo.findOne({ id: entity.id });
    expect(deleted).toBeNull();
  });
});
