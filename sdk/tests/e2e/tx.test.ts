import { describe, it, expect, beforeEach } from "vitest";
import { createTestClient, skipIfNoGateway, generateTableName } from "./setup";

describe("Transactions", () => {
  if (skipIfNoGateway()) {
    console.log("Skipping transaction tests");
  }

  let tableName: string;

  beforeEach(() => {
    tableName = generateTableName();
  });

  it("should execute transaction with multiple ops", async () => {
    const client = await createTestClient();

    // Create table
    await client.db.createTable(
      `CREATE TABLE ${tableName} (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT, balance INTEGER)`
    );

    // Execute transaction with multiple operations
    const results = await client.db.transaction([
      {
        kind: "exec",
        sql: `INSERT INTO ${tableName} (name, balance) VALUES (?, ?)`,
        args: ["User A", 100],
      },
      {
        kind: "exec",
        sql: `INSERT INTO ${tableName} (name, balance) VALUES (?, ?)`,
        args: ["User B", 200],
      },
      {
        kind: "query",
        sql: `SELECT COUNT(*) as count FROM ${tableName}`,
        args: [],
      },
    ]);

    expect(results).toBeDefined();
  });

  it("should support query inside transaction", async () => {
    const client = await createTestClient();

    // Create table
    await client.db.createTable(
      `CREATE TABLE ${tableName} (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT, value INTEGER)`
    );

    // Pre-insert data
    await client.db.exec(
      `INSERT INTO ${tableName} (name, value) VALUES (?, ?)`,
      ["item1", 10]
    );

    // Transaction with insert and query
    const results = await client.db.transaction([
      {
        kind: "exec",
        sql: `INSERT INTO ${tableName} (name, value) VALUES (?, ?)`,
        args: ["item2", 20],
      },
      {
        kind: "query",
        sql: `SELECT SUM(value) as total FROM ${tableName}`,
        args: [],
      },
    ]);

    expect(results).toBeDefined();
  });
});
