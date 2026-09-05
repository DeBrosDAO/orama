/**
 * API-key grants, mirrored from the gateway.
 *
 * A key carries a set of these. The gateway refuses an operation whose grant
 * the key does not hold with a 403 that names the grant, which the SDK raises
 * as a `ScopeError` with `requiredScope` set. None of this was visible from
 * TypeScript before: the word "scope" did not appear anywhere in the SDK, so a
 * 403 was an opaque string and the only way to find out what a key needed was
 * to read the Go source.
 *
 * These constants are checked against `core/pkg/gateway/auth/scopes.go` by a
 * test, because a client that disagrees with the gateway about what grants
 * exist is worse than one that says nothing.
 */

/** Every grant the gateway will mint. */
export const SCOPES = [
  "admin",
  "cache",
  "invoke",
  "proxy",
  "pubsub",
  "push",
  "storage",
  "webrtc",
] as const;

export type Scope = (typeof SCOPES)[number];

/**
 * Grants that are safe in a public client bundle. `admin` is the whole
 * control plane — deploys, secrets, migrations, key management, raw RQLite —
 * and belongs only in CI and on a developer's machine.
 */
export const DATA_PLANE_SCOPES: readonly Scope[] = [
  "cache",
  "invoke",
  "proxy",
  "pubsub",
  "push",
  "storage",
  "webrtc",
];

/**
 * Named tiers accepted when minting a key. `runtime` and `app` are aliases of
 * `app-runtime`; `invoke` is an alias of `invoke-only`.
 */
export const KEY_PROFILES = [
  "admin",
  "app-runtime",
  "runtime",
  "app",
  "invoke-only",
  "invoke",
] as const;

export type KeyProfile = (typeof KEY_PROFILES)[number];

/** What each profile grants. */
export const PROFILE_SCOPES: Readonly<Record<KeyProfile, readonly Scope[]>> = {
  admin: ["admin"],
  "app-runtime": ["invoke", "storage", "push", "webrtc", "proxy"],
  runtime: ["invoke", "storage", "push", "webrtc", "proxy"],
  app: ["invoke", "storage", "push", "webrtc", "proxy"],
  "invoke-only": ["invoke"],
  invoke: ["invoke"],
};

/** Whether `value` is a grant this gateway knows. */
export function isScope(value: unknown): value is Scope {
  return typeof value === "string" && (SCOPES as readonly string[]).includes(value);
}

/**
 * Whether a held grant set satisfies a required grant, by the gateway's rule:
 * `admin` satisfies everything, and an empty requirement is satisfied by any
 * valid credential.
 */
export function satisfiesScope(held: readonly Scope[], required: Scope | "" | undefined): boolean {
  if (!required) return true;
  if (held.includes("admin")) return true;
  return held.includes(required);
}

/**
 * A member's role in a namespace, mirrored from the gateway.
 *
 * A role is a grant set: `owner` and `admin` reach the control plane, `runtime`
 * the data plane, `reader` nothing beyond the routes that ask for no grant.
 * Ownership is transferred rather than granted — a namespace with no owner is
 * claimable by whoever signs in to it next.
 */
export const ROLES = ["owner", "admin", "runtime", "reader"] as const;

export type Role = (typeof ROLES)[number];

/** The roles that can be handed out. Ownership is transferred instead. */
export const GRANTABLE_ROLES: readonly Role[] = ["admin", "runtime", "reader"];
