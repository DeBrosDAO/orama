import { HttpClient } from "../core/http";
import { QueryBuilder } from "./qb";
import { Repository } from "./repository";
import {
  QueryResponse,
  TransactionOp,
  TransactionRequest,
  Entity,
  FindOptions,
} from "./types";

export class DBClient {
  private httpClient: HttpClient;

  constructor(httpClient: HttpClient) {
    this.httpClient = httpClient;
  }

  /**
   * Execute a write/DDL SQL statement.
   */
  async exec(
    sql: string,
    args: any[] = []
  ): Promise<{ rows_affected: number; last_insert_id?: number }> {
    return this.httpClient.post("/v1/rqlite/exec", { sql, args });
  }

  /**
   * Execute a SELECT query.
   */
  async query<T = any>(sql: string, args: any[] = []): Promise<T[]> {
    const response = await this.httpClient.post<QueryResponse>(
      "/v1/rqlite/query",
      { sql, args }
    );
    return response.items || [];
  }

  /**
   * Find rows with map-based criteria.
   */
  async find<T = any>(
    table: string,
    criteria: Record<string, any> = {},
    options: FindOptions = {}
  ): Promise<T[]> {
    const response = await this.httpClient.post<QueryResponse>(
      "/v1/rqlite/find",
      {
        table,
        criteria,
        options,
      }
    );
    return response.items || [];
  }

  /**
   * Find a single row with map-based criteria.
   */
  async findOne<T = any>(
    table: string,
    criteria: Record<string, any>
  ): Promise<T | null> {
    return this.httpClient.post<T | null>("/v1/rqlite/find-one", {
      table,
      criteria,
    });
  }

  /**
   * Create a fluent QueryBuilder for complex SELECT queries.
   */
  createQueryBuilder(table: string): QueryBuilder {
    return new QueryBuilder(this.httpClient, table);
  }

  /**
   * Create a Repository for entity-based operations.
   */
  repository<T extends Record<string, any>>(
    tableName: string,
    primaryKey = "id"
  ): Repository<T> {
    return new Repository(this.httpClient, tableName, primaryKey);
  }

  /**
   * Execute multiple operations atomically.
   */
  async transaction(
    ops: TransactionOp[],
    returnResults = true
  ): Promise<any[]> {
    const response = await this.httpClient.post<{ results?: any[] }>(
      "/v1/rqlite/transaction",
      {
        ops,
        return_results: returnResults,
      }
    );
    return response.results || [];
  }

  /**
   * Create a table from DDL SQL.
   */
  async createTable(schema: string): Promise<void> {
    await this.httpClient.post("/v1/rqlite/create-table", { schema });
  }

  /**
   * Drop a table.
   */
  async dropTable(table: string): Promise<void> {
    await this.httpClient.post("/v1/rqlite/drop-table", { table });
  }

  /**
   * Get current database schema.
   */
  async getSchema(): Promise<any> {
    return this.httpClient.get("/v1/rqlite/schema");
  }
}
