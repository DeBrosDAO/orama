export interface ChangelogEntry {
  version: string;
  date: string;
  summary: string;
  changes: Array<{
    type: "added" | "changed" | "fixed" | "removed";
    text: string;
  }>;
}

export const CHANGELOG: ChangelogEntry[] = [
  {
    version: "2.0.0",
    date: "2026-02-15",
    summary: "Major release with Vault integration and serverless WASM",
    changes: [
      { type: "added", text: "Orama Vault integration with Shamir's Secret Sharing" },
      { type: "added", text: "Serverless WASM function execution" },
      { type: "added", text: "TypeScript SDK v0.7 with vault and functions modules" },
      { type: "changed", text: "Gateway architecture refactored for modular service loading" },
      { type: "changed", text: "RQLite upgraded to latest version with improved Raft performance" },
      { type: "fixed", text: "WebSocket reconnection stability in pub/sub subscriptions" },
    ],
  },
  {
    version: "1.5.0",
    date: "2026-01-20",
    summary: "Distributed cache and improved pub/sub",
    changes: [
      { type: "added", text: "Olric distributed cache integration" },
      { type: "added", text: "Presence tracking for pub/sub topics" },
      { type: "changed", text: "Pub/sub message routing optimized for large clusters" },
      { type: "fixed", text: "Namespace isolation edge case in multi-tenant deployments" },
    ],
  },
  {
    version: "1.0.0",
    date: "2025-11-01",
    summary: "Initial stable release",
    changes: [
      { type: "added", text: "Gateway with RQLite backend" },
      { type: "added", text: "LibP2P peer discovery and mesh networking" },
      { type: "added", text: "WireGuard overlay network" },
      { type: "added", text: "API key and JWT authentication" },
      { type: "added", text: "TypeScript SDK v0.5" },
      { type: "added", text: "IPFS storage integration" },
    ],
  },
];
