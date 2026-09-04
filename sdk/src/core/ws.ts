import WebSocket from "isomorphic-ws";
import { SDKError } from "../errors";
import { NetworkErrorCallback } from "./http";
import { Logger } from "./logger";

export interface WSClientConfig {
  wsURL: string;
  timeout?: number;
  authToken?: string;
  WebSocket?: typeof WebSocket;
  /**
   * Callback invoked on WebSocket errors.
   * Use this to trigger gateway failover at the application layer.
   */
  onNetworkError?: NetworkErrorCallback;
  /**
   * Where to write debug lines. Defaults to a logger that prints nothing:
   * a WebSocket client built directly, rather than through `createClient`,
   * has no `debug` setting to inherit.
   */
  logger?: Logger;
}

export type WSMessageHandler = (data: string) => void;
export type WSErrorHandler = (error: Error) => void;
export type WSCloseHandler = (code: number, reason: string) => void;
export type WSOpenHandler = () => void;

/**
 * Simple WebSocket client with minimal abstractions
 * No complex reconnection, no failover - keep it simple
 * Gateway failover is handled at the application layer
 */
export class WSClient {
  private wsURL: string;
  private timeout: number;
  private authToken?: string;
  private WebSocketClass: typeof WebSocket;
  private onNetworkError?: NetworkErrorCallback;
  private readonly log: Logger;

  private ws?: WebSocket;
  private messageHandlers: Set<WSMessageHandler> = new Set();
  private errorHandlers: Set<WSErrorHandler> = new Set();
  private closeHandlers: Set<WSCloseHandler> = new Set();
  private openHandlers: Set<WSOpenHandler> = new Set();
  private isClosed = false;

  constructor(config: WSClientConfig) {
    this.wsURL = config.wsURL;
    this.timeout = config.timeout ?? 30000;
    this.authToken = config.authToken;
    this.WebSocketClass = config.WebSocket ?? WebSocket;
    this.onNetworkError = config.onNetworkError;
    this.log = config.logger ?? Logger.disabled();
  }

  /**
   * Set the network error callback
   */
  setOnNetworkError(callback: NetworkErrorCallback | undefined): void {
    this.onNetworkError = callback;
  }

  /**
   * Get the current WebSocket URL
   */
  get url(): string {
    return this.wsURL;
  }

  /**
   * Connect to WebSocket server
   */
  connect(): Promise<void> {
    return new Promise((resolve, reject) => {
      try {
        const wsUrl = this.buildWSUrl();
        this.ws = new this.WebSocketClass(wsUrl);
        this.isClosed = false;

        const timeout = setTimeout(() => {
          this.ws?.close();
          const error = new SDKError("WebSocket connection timeout", 408, "WS_TIMEOUT");

          // Call the network error callback if configured
          if (this.onNetworkError) {
            this.onNetworkError(error, {
              method: "WS",
              path: this.wsURL,
              isRetry: false,
              attempt: 0,
            });
          }

          reject(error);
        }, this.timeout);

        this.ws.addEventListener("open", () => {
          clearTimeout(timeout);
          this.log.log(`Connected to ${this.wsURL}`);
          this.openHandlers.forEach((handler) => handler());
          resolve();
        });

        this.ws.addEventListener("message", (event: Event) => {
          const msgEvent = event as MessageEvent;
          this.messageHandlers.forEach((handler) => handler(msgEvent.data));
        });

        this.ws.addEventListener("error", (event: Event) => {
          this.log.error("WebSocket error:", event);
          clearTimeout(timeout);
          // Extract useful details from the event — raw Event objects don't serialize
          const details: Record<string, any> = { type: event.type };
          if ("message" in event) {
            details.message = (event as ErrorEvent).message;
          }
          const error = new SDKError("WebSocket error", 0, "WS_ERROR", details);

          // Call the network error callback if configured
          if (this.onNetworkError) {
            this.onNetworkError(error, {
              method: "WS",
              path: this.wsURL,
              isRetry: false,
              attempt: 0,
            });
          }

          this.errorHandlers.forEach((handler) => handler(error));
          reject(error);
        });

        this.ws.addEventListener("close", (event: Event) => {
          clearTimeout(timeout);
          const closeEvent = event as CloseEvent;
          const code = closeEvent.code ?? 1006;
          const reason = closeEvent.reason ?? "";
          this.log.log(
            `Connection closed (code: ${code}, reason: ${reason || "none"})`
          );
          this.closeHandlers.forEach((handler) => handler(code, reason));
        });
      } catch (error) {
        reject(error);
      }
    });
  }

  /**
   * Build WebSocket URL with auth token
   */
  private buildWSUrl(): string {
    let url = this.wsURL;

    if (this.authToken) {
      const separator = url.includes("?") ? "&" : "?";
      const paramName = this.authToken.startsWith("ak_") ? "api_key" : "token";
      // API keys contain a colon (ak_xxx:namespace) that must not be percent-encoded
      const encodedToken = this.authToken.startsWith("ak_")
        ? this.authToken
        : encodeURIComponent(this.authToken);
      url += `${separator}${paramName}=${encodedToken}`;
    }

    return url;
  }

  /**
   * Register message handler
   */
  onMessage(handler: WSMessageHandler): () => void {
    this.messageHandlers.add(handler);
    return () => this.messageHandlers.delete(handler);
  }

  /**
   * Unregister message handler
   */
  offMessage(handler: WSMessageHandler): void {
    this.messageHandlers.delete(handler);
  }

  /**
   * Register error handler
   */
  onError(handler: WSErrorHandler): () => void {
    this.errorHandlers.add(handler);
    return () => this.errorHandlers.delete(handler);
  }

  /**
   * Unregister error handler
   */
  offError(handler: WSErrorHandler): void {
    this.errorHandlers.delete(handler);
  }

  /**
   * Register close handler
   */
  onClose(handler: WSCloseHandler): () => void {
    this.closeHandlers.add(handler);
    return () => this.closeHandlers.delete(handler);
  }

  /**
   * Unregister close handler
   */
  offClose(handler: WSCloseHandler): void {
    this.closeHandlers.delete(handler);
  }

  /**
   * Register open handler
   */
  onOpen(handler: WSOpenHandler): () => void {
    this.openHandlers.add(handler);
    return () => this.openHandlers.delete(handler);
  }

  /**
   * Send data through WebSocket
   */
  send(data: string): void {
    if (this.ws?.readyState !== WebSocket.OPEN) {
      throw new SDKError("WebSocket is not connected", 0, "WS_NOT_CONNECTED");
    }
    this.ws.send(data);
  }

  /**
   * Close WebSocket connection
   */
  close(): void {
    if (this.isClosed) {
      return;
    }
    this.isClosed = true;
    this.ws?.close();
  }

  /**
   * Check if WebSocket is connected
   */
  isConnected(): boolean {
    return !this.isClosed && this.ws?.readyState === WebSocket.OPEN;
  }

  /**
   * Update auth token
   */
  setAuthToken(token?: string): void {
    this.authToken = token;
  }
}
