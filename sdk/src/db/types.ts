export interface Entity {
  TableName(): string;
}

export interface QueryResponse {
  columns?: string[];
  rows?: any[][];
  count?: number;
  items?: any[];
}

export interface TransactionOp {
  kind: "exec" | "query";
  sql: string;
  args?: any[];
}

export interface TransactionRequest {
  statements?: string[];
  ops?: TransactionOp[];
  return_results?: boolean;
}

export interface SelectOptions {
  select?: string[];
  joins?: Array<{
    kind: "INNER" | "LEFT" | "RIGHT" | "FULL";
    table: string;
    on: string;
  }>;
  where?: Array<{
    conj?: "AND" | "OR";
    expr: string;
    args?: any[];
  }>;
  group_by?: string[];
  order_by?: string[];
  limit?: number;
  offset?: number;
  one?: boolean;
}

export type FindOptions = Omit<SelectOptions, "select" | "joins" | "one">;

export interface ColumnDefinition {
  name: string;
  isPrimaryKey?: boolean;
  isAutoIncrement?: boolean;
}

export function extractTableName(entity: Entity | string): string {
  if (typeof entity === "string") return entity;
  return entity.TableName();
}

export function extractPrimaryKey(entity: any): string | undefined {
  if (typeof entity === "string") return undefined;

  // Check for explicit pk tag
  const metadata = (entity as any)._dbMetadata;
  if (metadata?.primaryKey) return metadata.primaryKey;

  // Check for ID field
  if (entity.id !== undefined) return "id";

  return undefined;
}
