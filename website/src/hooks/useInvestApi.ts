const API_BASE = import.meta.env.VITE_INVEST_API_URL || "http://localhost:8090";
const TOKEN_KEY = "orama_invest_token";

/* ── Types ── */

export interface Stats {
  tokens_sold: number;
  tokens_remaining: number;
  token_raised_btc: number;
  licenses_sold: number;
  licenses_left: number;
  license_raised_btc: number;
  total_raised_btc: number;
  whitelist_count: number;
}

export interface ActivityEntry {
  event_type: string;
  wallet: string;
  detail: string;
  created_at: string;
}

export interface LicenseInfo {
  license_number: number;
  claimed_via_nft: boolean;
  purchased_at: string;
}

export interface PurchaseRecord {
  type: string;
  amount: number;
  currency: string;
  tx_hash: string;
  created_at: string;
}

export interface MeResponse {
  wallet: string;
  chain: string;
  tokens_purchased: number;
  tokens_spent: number;
  licenses: LicenseInfo[];
  on_whitelist: boolean;
  purchase_history: PurchaseRecord[];
  anchat_claimed: boolean;
  anchat_orama_amount: number;
}

export interface NftStatus {
  has_team_nft: boolean;
  has_community_nft: boolean;
  nft_count: number;
}

export interface AnchatBalance {
  balance: number;
  claimable_orama: number;
}

export interface LoginResponse {
  token: string;
  wallet: string;
  chain: string;
  expires_at: string;
}

/* ── Token management ── */

export function getStoredToken(): string | null {
  return localStorage.getItem(TOKEN_KEY);
}

export function storeToken(token: string): void {
  localStorage.setItem(TOKEN_KEY, token);
}

export function clearToken(): void {
  localStorage.removeItem(TOKEN_KEY);
}

/* ── Headers ── */

function authHeaders(): HeadersInit {
  const h: HeadersInit = { "Content-Type": "application/json" };
  const token = getStoredToken();
  if (token) {
    h["Authorization"] = `Bearer ${token}`;
  }
  return h;
}

/* ── Public endpoints ── */

export interface Prices {
  btc_usd: number;
}

export async function fetchPrices(): Promise<Prices> {
  const res = await fetch(`${API_BASE}/api/price`);
  if (!res.ok) throw new Error("Failed to fetch prices");
  return res.json();
}

export async function fetchStats(): Promise<Stats> {
  const res = await fetch(`${API_BASE}/api/stats`);
  if (!res.ok) throw new Error("Failed to fetch stats");
  return res.json();
}

export async function fetchActivity(): Promise<ActivityEntry[]> {
  const res = await fetch(`${API_BASE}/api/activity`);
  if (!res.ok) throw new Error("Failed to fetch activity");
  return res.json();
}

/* ── Auth ── */

export async function login(
  wallet: string,
  chain: string,
  message: string,
  signature: string,
): Promise<LoginResponse> {
  const res = await fetch(`${API_BASE}/api/auth/login`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ wallet, chain, message, signature }),
  });
  if (!res.ok) {
    const err = await res.json();
    throw new Error(err.error || "Login failed");
  }
  const data: LoginResponse = await res.json();
  storeToken(data.token);
  return data;
}

/* ── Authenticated endpoints ── */

export async function fetchMe(): Promise<MeResponse> {
  const res = await fetch(`${API_BASE}/api/me`, {
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error("Failed to fetch user data");
  return res.json();
}

export async function purchaseToken(
  data: { amount_paid: number; pay_currency: string; tx_hash: string },
): Promise<{ tokens_allocated: number; tx_hash: string }> {
  const res = await fetch(`${API_BASE}/api/purchase/token`, {
    method: "POST",
    headers: authHeaders(),
    body: JSON.stringify(data),
  });
  if (!res.ok) {
    const err = await res.json();
    throw new Error(err.error || "Purchase failed");
  }
  return res.json();
}

export async function purchaseLicense(
  data: { amount_paid: number; pay_currency: string; tx_hash: string; claimed_via_nft?: boolean },
): Promise<{ license_number: number; claimed_via_nft: boolean }> {
  const res = await fetch(`${API_BASE}/api/purchase/license`, {
    method: "POST",
    headers: authHeaders(),
    body: JSON.stringify(data),
  });
  if (!res.ok) {
    const err = await res.json();
    throw new Error(err.error || "License purchase failed");
  }
  return res.json();
}

export async function joinWhitelist(): Promise<{ position: number }> {
  const res = await fetch(`${API_BASE}/api/whitelist/join`, {
    method: "POST",
    headers: authHeaders(),
  });
  if (!res.ok) {
    const err = await res.json();
    throw new Error(err.error || "Failed to join whitelist");
  }
  return res.json();
}

export async function fetchNftStatus(): Promise<NftStatus> {
  const res = await fetch(`${API_BASE}/api/nft/status`, {
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error("Failed to fetch NFT status");
  return res.json();
}

export async function fetchAnchatBalance(): Promise<AnchatBalance> {
  const res = await fetch(`${API_BASE}/api/anchat/balance`, {
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error("Failed to fetch $ANCHAT balance");
  return res.json();
}

export async function claimAnchatOrama(): Promise<{ orama_amount: number }> {
  const res = await fetch(`${API_BASE}/api/anchat/claim`, {
    method: "POST",
    headers: authHeaders(),
  });
  if (!res.ok) {
    const err = await res.json();
    throw new Error(err.error || "Claim failed");
  }
  return res.json();
}
