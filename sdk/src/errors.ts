import type { Scope } from "./scopes";

/**
 * Every failure the SDK raises is an `SDKError`, so a caller can keep one
 * `catch` and branch on `code`/`httpStatus`. The subclasses below let a caller
 * branch with `instanceof` instead, for the four cases an application actually
 * handles differently: the credential is wrong, the credential is right but too
 * narrow, the thing is not there, and the gateway was never reached.
 *
 * `SDKError.fromResponse` picks the subclass, so existing `catch (e) { if (e
 * instanceof SDKError) … }` code is unaffected.
 */
export class SDKError extends Error {
  public readonly httpStatus: number;
  public readonly code: string;
  public readonly details: Record<string, any>;

  constructor(
    message: string,
    httpStatus: number = 500,
    code: string = "SDK_ERROR",
    details: Record<string, any> = {}
  ) {
    super(message);
    this.name = "SDKError";
    this.httpStatus = httpStatus;
    this.code = code;
    this.details = details;
  }

  /**
   * Build the right error for a gateway response.
   *
   * The gateway's error body is `{error, code?, …}`; anything else in it is
   * kept in `details`, which is where a structured hint like `required_scope`
   * arrives.
   */
  static fromResponse(status: number, body: any, message?: string): SDKError {
    const errorMsg = message || body?.error || `HTTP ${status}`;
    const code = body?.code || `HTTP_${status}`;
    const details = body && typeof body === "object" ? body : {};

    if (status === 401) {
      if (code === AuthCode.Revoked) {
        return new RevokedCredentialError(errorMsg, status, code, details);
      }
      return new AuthError(errorMsg, status, code, details);
    }
    if (status === 403) {
      return new ScopeError(errorMsg, status, code, details);
    }
    if (status === 404) {
      return new NotFoundError(errorMsg, status, code, details);
    }
    return new SDKError(errorMsg, status, code, details);
  }

  /**
   * What to do about it, when the gateway said. Every refusal carries one: the
   * code says what happened, the hint says what to do next.
   */
  get hint(): string | undefined {
    return this.details?.hint;
  }

  toJSON() {
    return {
      name: this.name,
      message: this.message,
      httpStatus: this.httpStatus,
      code: this.code,
      details: this.details,
    };
  }
}

/**
 * The codes the gateway puts on a refusal.
 *
 * A 401 had at least six causes and told them apart only by an English string,
 * so nothing could distinguish "you sent nothing" from "your key was revoked"
 * without matching on prose. Switch on `error.code` against these.
 *
 * Mirrors `core/pkg/gateway/auth_errors.go`; the list only ever grows.
 */
export const AuthCode = {
  /** No credential was presented. */
  Missing: "AUTH_MISSING",
  /** A credential was presented and is not one this cluster knows. */
  InvalidKey: "AUTH_INVALID_KEY",
  /** The credential or session was revoked. Sign in again. */
  Revoked: "AUTH_REVOKED",
  /** The credential expired. Refresh, or sign in again. */
  Expired: "AUTH_EXPIRED",
  /** The grant is held but the operation needs a logged-in user. */
  UserLoginRequired: "USER_JWT_REQUIRED",
  /** The credential lacks the grant named in `requiredScope`. */
  ScopeMissing: "INSUFFICIENT_SCOPE",
  /** The credential belongs to another namespace. */
  NamespaceMismatch: "NAMESPACE_MISMATCH",
  /** The credential is not an owner of this namespace. */
  OwnershipRequired: "OWNERSHIP_REQUIRED",
  /** The route is the cluster operator's. */
  OperatorRequired: "NOT_AN_OPERATOR",
  /** The destination is refused; a different credential will not help. */
  DestinationNotAllowed: "DESTINATION_NOT_ALLOWED",

  // --- Signing in with a wallet ------------------------------------------
  /** The sign-in message could not be read. Send the one `challenge()` returned, verbatim. */
  MessageMalformed: "AUTH_MESSAGE_MALFORMED",
  /** The message was signed for another host. Ask this gateway for the challenge. */
  DomainMismatch: "AUTH_DOMAIN_MISMATCH",
  /** The message's own expiry has passed. Ask for a new challenge. */
  MessageExpired: "AUTH_MESSAGE_EXPIRED",
  /** The message is fine and the signature over it is not. */
  SignatureInvalid: "AUTH_SIGNATURE_INVALID",
  /**
   * The challenge cannot be claimed: never issued, already used, or expired.
   *
   * One code for three causes on purpose — telling them apart would say which
   * wallets hold outstanding challenges, and the answer is the same in all
   * three: ask for a new one.
   */
  ChallengeInvalid: "AUTH_CHALLENGE_INVALID",
} as const;

export type AuthCode = (typeof AuthCode)[keyof typeof AuthCode];

/**
 * The credential was missing, malformed, expired, or is not enough on its own.
 *
 * The gateway also answers 401 when a data-plane grant needs a logged-in user
 * and only an API key was sent; that case carries the code `USER_JWT_REQUIRED`
 * and names the grant in `requiredScope`.
 */
export class AuthError extends SDKError {
  constructor(
    message: string,
    httpStatus: number = 401,
    code: string = "UNAUTHORIZED",
    details: Record<string, any> = {}
  ) {
    super(message, httpStatus, code, details);
    this.name = "AuthError";
  }

  /** The grant the operation needs, when the gateway named one. */
  get requiredScope(): Scope | undefined {
    return this.details?.required_scope;
  }
}

/**
 * The credential is valid but its grants do not cover the operation.
 *
 * `requiredScope` is the grant to ask for. It comes from the gateway's
 * `required_scope` field rather than from parsing the message.
 */
export class ScopeError extends SDKError {
  constructor(
    message: string,
    httpStatus: number = 403,
    code: string = "FORBIDDEN",
    details: Record<string, any> = {}
  ) {
    super(message, httpStatus, code, details);
    this.name = "ScopeError";
  }

  /** The grant the operation needs, when the gateway named one. */
  get requiredScope(): Scope | undefined {
    return this.details?.required_scope;
  }
}

/** The addressed thing does not exist. */
export class NotFoundError extends SDKError {
  constructor(
    message: string,
    httpStatus: number = 404,
    code: string = "NOT_FOUND",
    details: Record<string, any> = {}
  ) {
    super(message, httpStatus, code, details);
    this.name = "NotFoundError";
  }
}

/**
 * No HTTP response was received: DNS failure, connection refused, TLS failure,
 * offline, a timeout, or a caller's abort. `httpStatus` is 0 in every case,
 * which is how "could not reach the gateway" is told apart from a real 4xx/5xx.
 */
export class NetworkError extends SDKError {
  constructor(
    message: string,
    code: string = "NETWORK_ERROR",
    details: Record<string, any> = {}
  ) {
    super(message, 0, code, details);
    this.name = "NetworkError";
  }
}

/**
 * The credential or the session was revoked, so retrying with it will never
 * work. Distinct from every other 401 because the answer is "sign in again"
 * rather than "check what you sent".
 */
export class RevokedCredentialError extends AuthError {
  constructor(
    message: string,
    httpStatus: number = 401,
    code: string = AuthCode.Revoked,
    details: Record<string, any> = {}
  ) {
    super(message, httpStatus, code, details);
    this.name = "RevokedCredentialError";
  }
}
