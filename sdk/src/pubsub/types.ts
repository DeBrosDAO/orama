export interface PubSubMessage {
  /**
   * The payload decoded as UTF-8 text.
   *
   * Lossy for a payload that is not text: bytes that do not form valid UTF-8
   * are replaced with U+FFFD. `publish` accepts a `Uint8Array`, so a binary
   * message could be sent and never read back — use `bytes` for those.
   */
  data: string;
  /** The payload exactly as it was published. */
  bytes: Uint8Array;
  topic: string;
  timestamp: number;
}

export interface RawEnvelope {
  type?: string;
  data: string; // base64-encoded
  timestamp: number;
  topic: string;
  member_id?: string;
  meta?: Record<string, unknown>;
}

export interface PresenceMember {
  memberId: string;
  joinedAt: number;
  meta?: Record<string, unknown>;
}

export interface PresenceResponse {
  topic: string;
  members: PresenceMember[];
  count: number;
}

export interface PresenceOptions {
  enabled: boolean;
  memberId: string;
  meta?: Record<string, unknown>;
  onJoin?: (member: PresenceMember) => void;
  onLeave?: (member: PresenceMember) => void;
}

export interface SubscribeOptions {
  onMessage?: MessageHandler;
  onError?: ErrorHandler;
  onClose?: CloseHandler;
  presence?: PresenceOptions;
}

export type MessageHandler = (message: PubSubMessage) => void;
export type ErrorHandler = (error: Error) => void;
export type CloseHandler = (code: number, reason: string) => void;

