import { HttpClient } from "../core/http";
import { Logger } from "../core/logger";
import { WSClient, WSClientConfig } from "../core/ws";
import {
  PubSubMessage,
  RawEnvelope,
  MessageHandler,
  ErrorHandler,
  CloseHandler,
  SubscribeOptions,
  PresenceResponse,
  PresenceMember,
  PresenceOptions,
} from "./types";

// Cross-platform base64 encoding/decoding utilities
function base64Encode(str: string): string {
  if (typeof Buffer !== "undefined") {
    return Buffer.from(str).toString("base64");
  } else if (typeof btoa !== "undefined") {
    return btoa(
      encodeURIComponent(str).replace(/%([0-9A-F]{2})/g, (match, p1) =>
        String.fromCharCode(parseInt(p1, 16))
      )
    );
  }
  throw new Error("No base64 encoding method available");
}

function base64EncodeBytes(bytes: Uint8Array): string {
  if (typeof Buffer !== "undefined") {
    return Buffer.from(bytes).toString("base64");
  } else if (typeof btoa !== "undefined") {
    let binary = "";
    for (let i = 0; i < bytes.length; i++) {
      binary += String.fromCharCode(bytes[i]);
    }
    return btoa(binary);
  }
  throw new Error("No base64 encoding method available");
}

/**
 * Decode a base64 payload to the bytes that were published.
 *
 * The receive path used to go straight to a UTF-8 string, so a `Uint8Array`
 * published through `publish` — which the README advertises and the publish
 * path supports — came back through a lossy text decode and could not be
 * recovered. Decoding to bytes is what the wire actually carries; the text view
 * is derived from it.
 */
function base64DecodeBytes(b64: string): Uint8Array {
  if (typeof Buffer !== "undefined") {
    const buf = Buffer.from(b64, "base64");
    return new Uint8Array(buf.buffer, buf.byteOffset, buf.byteLength);
  }
  if (typeof atob !== "undefined") {
    const binary = atob(b64);
    const bytes = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i++) {
      bytes[i] = binary.charCodeAt(i);
    }
    return bytes;
  }
  throw new Error("No base64 decoding method available");
}

/**
 * Simple PubSub client - one WebSocket connection per topic
 * Gateway failover is handled at the application layer
 */
export class PubSubClient {
  private httpClient: HttpClient;
  private wsConfig: Partial<WSClientConfig>;

  constructor(httpClient: HttpClient, wsConfig: Partial<WSClientConfig> = {}) {
    this.httpClient = httpClient;
    this.wsConfig = wsConfig;
  }

  /**
   * Publish a message to a topic via HTTP
   */
  async publish(topic: string, data: string | Uint8Array): Promise<void> {
    let dataBase64: string;
    if (typeof data === "string") {
      dataBase64 = base64Encode(data);
    } else {
      dataBase64 = base64EncodeBytes(data);
    }

    await this.httpClient.post(
      "/v1/pubsub/publish",
      {
        topic,
        data_base64: dataBase64,
      },
      {
        timeout: 30000,
      }
    );
  }

  /**
   * List active topics in the current namespace
   */
  async topics(): Promise<string[]> {
    const response = await this.httpClient.get<{ topics: string[] }>(
      "/v1/pubsub/topics"
    );
    return response.topics || [];
  }

  /**
   * Get current presence for a topic without subscribing
   */
  async getPresence(topic: string): Promise<PresenceResponse> {
    const response = await this.httpClient.get<PresenceResponse>(
      `/v1/pubsub/presence?topic=${encodeURIComponent(topic)}`
    );
    return response;
  }

  /**
   * Subscribe to a topic via WebSocket
   * Creates one WebSocket connection per topic
   */
  async subscribe(
    topic: string,
    options: SubscribeOptions = {}
  ): Promise<Subscription> {
    // Build WebSocket URL for this topic
    const wsUrl = new URL(this.wsConfig.wsURL || "ws://127.0.0.1:6001");
    wsUrl.pathname = "/v1/pubsub/ws";
    wsUrl.searchParams.set("topic", topic);

    // Handle presence options
    let presence: PresenceOptions | undefined;
    if (options.presence?.enabled) {
      presence = options.presence;
      wsUrl.searchParams.set("presence", "true");
      wsUrl.searchParams.set("member_id", presence.memberId);
      if (presence.meta) {
        wsUrl.searchParams.set("member_meta", JSON.stringify(presence.meta));
      }
    }

    // A token, never the key. A browser cannot set a header on an upgrade, so
    // whatever goes here ends up in the gateway's access log and in the
    // browser's own history — which is exactly where a 90-day credential
    // should not be. The token expires in fifteen minutes.
    await this.httpClient.ensureCredential();
    const authToken = this.httpClient.getToken();

    // Create WebSocket client
    const wsClient = new WSClient({
      ...this.wsConfig,
      wsURL: wsUrl.toString(),
      authToken,
      logger: this.httpClient.logger("WSClient"),
    });

    await wsClient.connect();

    // Create subscription wrapper
    const subscription = new Subscription(
      wsClient,
      topic,
      presence,
      () => this.getPresence(topic),
      this.httpClient.logger("Subscription")
    );

    if (options.onMessage) {
      subscription.onMessage(options.onMessage);
    }
    if (options.onError) {
      subscription.onError(options.onError);
    }
    if (options.onClose) {
      subscription.onClose(options.onClose);
    }

    return subscription;
  }
}

/**
 * Subscription represents an active WebSocket subscription to a topic
 */
export class Subscription {
  private wsClient: WSClient;
  private topic: string;
  private presenceOptions?: PresenceOptions;
  private messageHandlers: Set<MessageHandler> = new Set();
  private errorHandlers: Set<ErrorHandler> = new Set();
  private closeHandlers: Set<CloseHandler> = new Set();
  private isClosed = false;
  private wsMessageHandler: ((data: string) => void) | null = null;
  private wsErrorHandler: ((error: Error) => void) | null = null;
  private wsCloseHandler: ((code: number, reason: string) => void) | null = null;
  private getPresenceFn: () => Promise<PresenceResponse>;
  private readonly log: Logger;

  /**
   * @param logger Defaults to a logger that prints nothing. `PubSubClient`
   * always supplies one carrying the client's `debug` setting; the parameter
   * is optional only because `Subscription` is exported and so can be built
   * directly.
   */
  constructor(
    wsClient: WSClient,
    topic: string,
    presenceOptions: PresenceOptions | undefined,
    getPresenceFn: () => Promise<PresenceResponse>,
    logger: Logger = Logger.disabled()
  ) {
    this.wsClient = wsClient;
    this.topic = topic;
    this.presenceOptions = presenceOptions;
    this.getPresenceFn = getPresenceFn;
    this.log = logger;

    // Register message handler
    this.wsMessageHandler = (data) => {
      try {
        // Parse gateway JSON envelope: {data: base64String, timestamp, topic}
        const envelope: RawEnvelope = JSON.parse(data);

        // Validate envelope structure
        if (!envelope || typeof envelope !== "object") {
          throw new Error("Invalid envelope: not an object");
        }

        // Handle presence events
        if (
          envelope.type === "presence.join" ||
          envelope.type === "presence.leave"
        ) {
          if (!envelope.member_id) {
            this.log.warn("Presence event missing member_id");
            return;
          }

          const presenceMember: PresenceMember = {
            memberId: envelope.member_id,
            joinedAt: envelope.timestamp,
            meta: envelope.meta,
          };

          if (
            envelope.type === "presence.join" &&
            this.presenceOptions?.onJoin
          ) {
            this.presenceOptions.onJoin(presenceMember);
          } else if (
            envelope.type === "presence.leave" &&
            this.presenceOptions?.onLeave
          ) {
            this.presenceOptions.onLeave(presenceMember);
          }
          return; // Don't call regular onMessage for presence events
        }

        if (!envelope.data || typeof envelope.data !== "string") {
          throw new Error("Invalid envelope: missing or invalid data field");
        }
        if (!envelope.topic || typeof envelope.topic !== "string") {
          throw new Error("Invalid envelope: missing or invalid topic field");
        }
        if (typeof envelope.timestamp !== "number") {
          throw new Error(
            "Invalid envelope: missing or invalid timestamp field"
          );
        }

        const bytes = base64DecodeBytes(envelope.data);

        const message: PubSubMessage = {
          topic: envelope.topic,
          bytes,
          data: new TextDecoder().decode(bytes),
          timestamp: envelope.timestamp,
        };

        this.log.log(`Received message on topic: ${this.topic}`);
        this.messageHandlers.forEach((handler) => handler(message));
      } catch (error) {
        this.log.error("Error processing message:", error);
        this.errorHandlers.forEach((handler) =>
          handler(error instanceof Error ? error : new Error(String(error)))
        );
      }
    };

    this.wsClient.onMessage(this.wsMessageHandler);

    // Register error handler
    this.wsErrorHandler = (error) => {
      this.errorHandlers.forEach((handler) => handler(error));
    };
    this.wsClient.onError(this.wsErrorHandler);

    // Register close handler
    this.wsCloseHandler = (code: number, reason: string) => {
      this.closeHandlers.forEach((handler) => handler(code, reason));
    };
    this.wsClient.onClose(this.wsCloseHandler);
  }

  /**
   * Get current presence (requires presence.enabled on subscribe)
   */
  async getPresence(): Promise<PresenceMember[]> {
    if (!this.presenceOptions?.enabled) {
      throw new Error("Presence is not enabled for this subscription");
    }

    const response = await this.getPresenceFn();
    return response.members;
  }

  /**
   * Check if presence is enabled for this subscription
   */
  hasPresence(): boolean {
    return !!this.presenceOptions?.enabled;
  }

  /**
   * Register message handler
   */
  onMessage(handler: MessageHandler): () => void {
    this.messageHandlers.add(handler);
    return () => this.messageHandlers.delete(handler);
  }

  /**
   * Register error handler
   */
  onError(handler: ErrorHandler): () => void {
    this.errorHandlers.add(handler);
    return () => this.errorHandlers.delete(handler);
  }

  /**
   * Register close handler
   */
  onClose(handler: CloseHandler): () => void {
    this.closeHandlers.add(handler);
    return () => this.closeHandlers.delete(handler);
  }

  /**
   * Close subscription and underlying WebSocket
   */
  close(): void {
    if (this.isClosed) {
      return;
    }
    this.isClosed = true;

    // Remove handlers from WSClient
    if (this.wsMessageHandler) {
      this.wsClient.offMessage(this.wsMessageHandler);
      this.wsMessageHandler = null;
    }
    if (this.wsErrorHandler) {
      this.wsClient.offError(this.wsErrorHandler);
      this.wsErrorHandler = null;
    }
    if (this.wsCloseHandler) {
      this.wsClient.offClose(this.wsCloseHandler);
      this.wsCloseHandler = null;
    }

    // Clear all local handlers
    this.messageHandlers.clear();
    this.errorHandlers.clear();
    this.closeHandlers.clear();

    // Close WebSocket connection
    this.wsClient.close();
  }

  /**
   * Check if subscription is active
   */
  isConnected(): boolean {
    return !this.isClosed && this.wsClient.isConnected();
  }
}
