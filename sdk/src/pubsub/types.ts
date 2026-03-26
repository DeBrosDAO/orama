export interface PubSubMessage {
  data: string;
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

