export type CodePermission = "everybody" | "nobody" | "any_of" | "unspecified";

/** One stored wasm code. */
export interface CodeInfo {
  codeId: number;
  creator: string;
  /** Hex of the code's hash. */
  checksum: string;
  /** Who may instantiate it. */
  permission: CodePermission;
  /** The addresses allowed when the permission names some. */
  allowed: string[];
}

/** One contract instance. */
export interface ContractInfo {
  address: string;
  codeId: number;
  creator: string;
  /** The account that may migrate it, or null. */
  admin: string | null;
  label: string;
  createdHeight: number;
}

export type HistoryOperation = "init" | "migrate" | "genesis" | "unknown";

/** One entry of a contract's code history: instantiation, then each migration. */
export interface HistoryEntry {
  operation: HistoryOperation;
  codeId: number;
  height: number;
  /** The message the operation carried as text, cleaned and cut; null when it was not text. */
  message: string | null;
}

/** One key of a contract's raw storage. */
export interface StateEntry {
  /** The key as text when it is printable, else its hex. */
  key: string;
  /** The value as text when it is printable, else its size. */
  value: string;
}

const PERMISSION_TEXT: Record<CodePermission, string> = {
  everybody: "Anyone may instantiate it",
  nobody: "No one may instantiate it",
  any_of: "Only the listed addresses may instantiate it",
  unspecified: "Not stated",
};

export function permissionText(permission: CodePermission): string {
  return PERMISSION_TEXT[permission];
}

const OPERATION_TEXT: Record<HistoryOperation, string> = {
  init: "Instantiated",
  migrate: "Migrated",
  genesis: "Set at genesis",
  unknown: "Unknown operation",
};

export function operationText(operation: HistoryOperation): string {
  return OPERATION_TEXT[operation];
}
