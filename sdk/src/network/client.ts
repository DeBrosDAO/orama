import { HttpClient } from "../core/http";

export interface PeerInfo {
  id: string;
  addresses: string[];
  lastSeen?: string;
}

export interface NetworkStatus {
  node_id: string;
  connected: boolean;
  peer_count: number;
  database_size: number;
  uptime: number;
}

export interface ProxyRequest {
  url: string;
  method: string;
  headers?: Record<string, string>;
  body?: string;
}

export interface ProxyResponse {
  status_code: number;
  headers: Record<string, string>;
  body: string;
  error?: string;
}

export class NetworkClient {
  private httpClient: HttpClient;

  constructor(httpClient: HttpClient) {
    this.httpClient = httpClient;
  }

  /**
   * Check gateway health.
   */
  async health(): Promise<boolean> {
    try {
      await this.httpClient.get("/v1/health");
      return true;
    } catch {
      return false;
    }
  }

  /**
   * Get network status.
   */
  async status(): Promise<NetworkStatus> {
    const response = await this.httpClient.get<NetworkStatus>(
      "/v1/network/status"
    );
    return response;
  }

  /**
   * Get connected peers.
   */
  async peers(): Promise<PeerInfo[]> {
    const response = await this.httpClient.get<{ peers: PeerInfo[] }>(
      "/v1/network/peers"
    );
    return response.peers || [];
  }

  /**
   * Connect to a peer.
   */
  async connect(peerAddr: string): Promise<void> {
    await this.httpClient.post("/v1/network/connect", { peer_addr: peerAddr });
  }

  /**
   * Disconnect from a peer.
   */
  async disconnect(peerId: string): Promise<void> {
    await this.httpClient.post("/v1/network/disconnect", { peer_id: peerId });
  }

  /**
   * Proxy an HTTP request through the Anyone network.
   * Requires authentication (API key or JWT).
   *
   * @param request - The proxy request configuration
   * @returns The proxied response
   * @throws {SDKError} If the Anyone proxy is not available or the request fails
   *
   * @example
   * ```ts
   * const response = await client.network.proxyAnon({
   *   url: 'https://api.example.com/data',
   *   method: 'GET',
   *   headers: {
   *     'Accept': 'application/json'
   *   }
   * });
   *
   * console.log(response.status_code); // 200
   * console.log(response.body); // Response data
   * ```
   */
  async proxyAnon(request: ProxyRequest): Promise<ProxyResponse> {
    const response = await this.httpClient.post<ProxyResponse>(
      "/v1/proxy/anon",
      request
    );

    // Check if the response contains an error
    if (response.error) {
      throw new Error(`Proxy request failed: ${response.error}`);
    }

    return response;
  }
}
