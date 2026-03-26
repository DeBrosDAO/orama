import { HttpClient } from "../core/http";
import { SelectOptions, QueryResponse } from "./types";

export class QueryBuilder {
  private httpClient: HttpClient;
  private table: string;
  private options: SelectOptions = {};

  constructor(httpClient: HttpClient, table: string) {
    this.httpClient = httpClient;
    this.table = table;
  }

  select(...columns: string[]): this {
    this.options.select = columns;
    return this;
  }

  innerJoin(table: string, on: string): this {
    if (!this.options.joins) this.options.joins = [];
    this.options.joins.push({ kind: "INNER", table, on });
    return this;
  }

  leftJoin(table: string, on: string): this {
    if (!this.options.joins) this.options.joins = [];
    this.options.joins.push({ kind: "LEFT", table, on });
    return this;
  }

  rightJoin(table: string, on: string): this {
    if (!this.options.joins) this.options.joins = [];
    this.options.joins.push({ kind: "RIGHT", table, on });
    return this;
  }

  where(expr: string, args?: any[]): this {
    if (!this.options.where) this.options.where = [];
    this.options.where.push({ conj: "AND", expr, args });
    return this;
  }

  andWhere(expr: string, args?: any[]): this {
    return this.where(expr, args);
  }

  orWhere(expr: string, args?: any[]): this {
    if (!this.options.where) this.options.where = [];
    this.options.where.push({ conj: "OR", expr, args });
    return this;
  }

  groupBy(...columns: string[]): this {
    this.options.group_by = columns;
    return this;
  }

  orderBy(...columns: string[]): this {
    this.options.order_by = columns;
    return this;
  }

  limit(n: number): this {
    this.options.limit = n;
    return this;
  }

  offset(n: number): this {
    this.options.offset = n;
    return this;
  }

  async getMany<T = any>(ctx?: any): Promise<T[]> {
    const response = await this.httpClient.post<QueryResponse>(
      "/v1/rqlite/select",
      {
        table: this.table,
        ...this.options,
      }
    );
    return response.items || [];
  }

  async getOne<T = any>(ctx?: any): Promise<T | null> {
    const response = await this.httpClient.post<QueryResponse>(
      "/v1/rqlite/select",
      {
        table: this.table,
        ...this.options,
        one: true,
        limit: 1,
      }
    );
    const items = response.items || [];
    return items.length > 0 ? items[0] : null;
  }

  async count(): Promise<number> {
    const response = await this.httpClient.post<QueryResponse>(
      "/v1/rqlite/select",
      {
        table: this.table,
        select: ["COUNT(*) AS count"],
        where: this.options.where,
        one: true,
      }
    );
    const items = response.items || [];
    return items.length > 0 ? items[0].count : 0;
  }
}
