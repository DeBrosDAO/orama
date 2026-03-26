/**
 * Functions Client
 * Client for calling serverless functions on the Orama Network
 */

import { HttpClient } from "../core/http";
import { SDKError } from "../errors";

export interface FunctionsClientConfig {
  /**
   * Base URL for the functions gateway
   * Defaults to using the same baseURL as the HTTP client
   */
  gatewayURL?: string;
  
  /**
   * Namespace for the functions
   */
  namespace: string;
}

export class FunctionsClient {
  private httpClient: HttpClient;
  private gatewayURL?: string;
  private namespace: string;

  constructor(httpClient: HttpClient, config?: FunctionsClientConfig) {
    this.httpClient = httpClient;
    this.gatewayURL = config?.gatewayURL;
    this.namespace = config?.namespace ?? "default";
  }

  /**
   * Invoke a serverless function by name
   * 
   * @param functionName - Name of the function to invoke
   * @param input - Input payload for the function
   * @returns The function response
   */
  async invoke<TInput = any, TOutput = any>(
    functionName: string,
    input: TInput
  ): Promise<TOutput> {
    const url = this.gatewayURL
      ? `${this.gatewayURL}/v1/invoke/${this.namespace}/${functionName}`
      : `/v1/invoke/${this.namespace}/${functionName}`;

    try {
      const response = await this.httpClient.post<TOutput>(url, input);
      return response;
    } catch (error) {
      if (error instanceof SDKError) {
        throw error;
      }
      throw new SDKError(
        `Function ${functionName} failed`,
        500,
        error instanceof Error ? error.message : String(error)
      );
    }
  }
}
