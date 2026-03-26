export const MOCK_ADDRESS = "0x7a3b...f29d";

export const MOCK_NAMESPACES = [
  { id: "ns-1", name: "my-project", cluster_status: "ready" as const },
  { id: "ns-2", name: "staging-env", cluster_status: "ready" as const },
  { id: "ns-3", name: "new-app", cluster_status: "provisioning" as const },
];

export const DEPLOYMENTS = [
  { name: "my-app", status: "active" as const, domain: "my-app.orama.network", lastDeploy: "2h ago", requests: "4.2K", type: "static" as const },
  { name: "api-service", status: "active" as const, domain: "api.orama.network", lastDeploy: "1d ago", requests: "8.1K", type: "go" as const },
  { name: "landing-page", status: "active" as const, domain: "landing.orama.network", lastDeploy: "3d ago", requests: "1.1K", type: "static" as const },
];

export const FUNCTIONS = [
  { name: "resize-image", runtime: "WASM", invocations: "2.3K", status: "active" as const },
  { name: "send-email", runtime: "WASM", invocations: "890", status: "active" as const },
];

export const STORAGE_FILES = [
  { name: "photo.jpg", cid: "bafybei...a3f2", size: "2.4 MB", pins: 3 },
  { name: "document.pdf", cid: "bafybei...b7c1", size: "1.1 MB", pins: 3 },
  { name: "bundle.wasm", cid: "bafybei...d4e9", size: "512 KB", pins: 3 },
];

export const SECRETS = [
  { name: "database-password", created: "2d ago", guardians: 5 },
  { name: "api-key-stripe", created: "1w ago", guardians: 5 },
  { name: "jwt-secret", created: "1w ago", guardians: 5 },
];

export const DOMAINS = [
  { domain: "myapp.com", target: "my-app.orama.network", status: "active" as const, tls: true },
  { domain: "api.myapp.com", target: "api.orama.network", status: "active" as const, tls: true },
];

export const TABLES = [
  { name: "users", rows: 1247, lastWrite: "5m ago" },
  { name: "sessions", rows: 89, lastWrite: "2m ago" },
  { name: "logs", rows: 45230, lastWrite: "1s ago" },
];

export const CACHE_KEYS = [
  { key: "session:abc", ttl: "3542s", size: "128B" },
  { key: "session:def", ttl: "1800s", size: "256B" },
  { key: "rate:10.0.0.1", ttl: "60s", size: "64B" },
];

export const NODES = [
  { name: "node-eu-1", ip: "10.0.0.1", status: "active" as const, uptime: "99.98%", region: "EU West", services: 5, healthy: 5 },
  { name: "node-us-1", ip: "10.0.0.2", status: "active" as const, uptime: "99.95%", region: "US East", services: 5, healthy: 5 },
  { name: "node-ap-1", ip: "10.0.0.3", status: "active" as const, uptime: "99.99%", region: "AP South", services: 5, healthy: 5 },
];

export const REWARDS_HISTORY = [
  { date: "Today", orama: "***", status: "Pending" as const },
  { date: "Yesterday", orama: "***", status: "Paid" as const },
  { date: "Feb 25", orama: "***", status: "Paid" as const },
];
