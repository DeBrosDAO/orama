/**
 * WebSocket Client abstraction interface
 * Provides a testable abstraction layer for WebSocket operations
 */
export interface IWebSocketClient {
  /**
   * Connect to WebSocket server
   */
  connect(): Promise<void>;

  /**
   * Close WebSocket connection
   */
  close(): void;

  /**
   * Send data through WebSocket
   */
  send(data: string): void;

  /**
   * Register message handler
   */
  onMessage(handler: (data: string) => void): void;

  /**
   * Unregister message handler
   */
  offMessage(handler: (data: string) => void): void;

  /**
   * Register error handler
   */
  onError(handler: (error: Error) => void): void;

  /**
   * Unregister error handler
   */
  offError(handler: (error: Error) => void): void;

  /**
   * Register close handler
   */
  onClose(handler: (code: number, reason: string) => void): void;

  /**
   * Unregister close handler
   */
  offClose(handler: (code: number, reason: string) => void): void;

  /**
   * Check if WebSocket is connected
   */
  isConnected(): boolean;

  /**
   * Get WebSocket URL
   */
  get url(): string;
}
