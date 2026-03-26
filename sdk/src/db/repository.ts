import { HttpClient } from "../core/http";
import { QueryBuilder } from "./qb";
import { QueryResponse, FindOptions } from "./types";
import { SDKError } from "../errors";

export class Repository<T extends Record<string, any>> {
  private httpClient: HttpClient;
  private tableName: string;
  private primaryKey: string;

  constructor(httpClient: HttpClient, tableName: string, primaryKey = "id") {
    this.httpClient = httpClient;
    this.tableName = tableName;
    this.primaryKey = primaryKey;
  }

  createQueryBuilder(): QueryBuilder {
    return new QueryBuilder(this.httpClient, this.tableName);
  }

  async find(
    criteria: Record<string, any> = {},
    options: FindOptions = {}
  ): Promise<T[]> {
    const response = await this.httpClient.post<QueryResponse>(
      "/v1/rqlite/find",
      {
        table: this.tableName,
        criteria,
        options,
      }
    );
    return response.items || [];
  }

  async findOne(criteria: Record<string, any>): Promise<T | null> {
    try {
      const response = await this.httpClient.post<T | null>(
        "/v1/rqlite/find-one",
        {
          table: this.tableName,
          criteria,
        }
      );
      return response;
    } catch (error) {
      // Return null if not found instead of throwing
      if (error instanceof SDKError && error.httpStatus === 404) {
        return null;
      }
      throw error;
    }
  }

  async save(entity: T): Promise<T> {
    const pkValue = entity[this.primaryKey];

    if (!pkValue) {
      // INSERT
      const response = await this.httpClient.post<{
        rows_affected: number;
        last_insert_id: number;
      }>("/v1/rqlite/exec", {
        sql: this.buildInsertSql(entity),
        args: this.buildInsertArgs(entity),
      });

      if (response.last_insert_id) {
        (entity as any)[this.primaryKey] = response.last_insert_id;
      }
      return entity;
    } else {
      // UPDATE
      await this.httpClient.post("/v1/rqlite/exec", {
        sql: this.buildUpdateSql(entity),
        args: this.buildUpdateArgs(entity),
      });
      return entity;
    }
  }

  async remove(entity: T | Record<string, any>): Promise<void> {
    const pkValue = entity[this.primaryKey];
    if (!pkValue) {
      throw new SDKError(
        `Primary key "${this.primaryKey}" is required for remove`,
        400,
        "MISSING_PK"
      );
    }

    await this.httpClient.post("/v1/rqlite/exec", {
      sql: `DELETE FROM ${this.tableName} WHERE ${this.primaryKey} = ?`,
      args: [pkValue],
    });
  }

  private buildInsertSql(entity: T): string {
    const columns = Object.keys(entity).filter((k) => entity[k] !== undefined);
    const placeholders = columns.map(() => "?").join(", ");
    return `INSERT INTO ${this.tableName} (${columns.join(
      ", "
    )}) VALUES (${placeholders})`;
  }

  private buildInsertArgs(entity: T): any[] {
    return Object.entries(entity)
      .filter(([, v]) => v !== undefined)
      .map(([, v]) => v);
  }

  private buildUpdateSql(entity: T): string {
    const columns = Object.keys(entity)
      .filter((k) => entity[k] !== undefined && k !== this.primaryKey)
      .map((k) => `${k} = ?`);
    return `UPDATE ${this.tableName} SET ${columns.join(", ")} WHERE ${
      this.primaryKey
    } = ?`;
  }

  private buildUpdateArgs(entity: T): any[] {
    const args = Object.entries(entity)
      .filter(([k, v]) => v !== undefined && k !== this.primaryKey)
      .map(([, v]) => v);
    args.push(entity[this.primaryKey]);
    return args;
  }
}
