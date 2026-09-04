/**
 * Functions Client
 * Client for calling serverless functions on the Orama Network
 */

import { HttpClient, RequestOptions } from "../core/http";
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
    input: TInput,
    options?: Pick<RequestOptions, "signal" | "timeout">
  ): Promise<TOutput> {
    const path = `/v1/invoke/${this.namespace}/${functionName}`;

    try {
      // `gatewayURL` is an origin, passed as an override rather than glued to
      // the front of the path: the path is appended to the client's base URL,
      // so an absolute URL here produced `http://localhost:10104https://…` and
      // every invoke against a separate functions gateway failed to parse.
      return await this.httpClient.post<TOutput>(path, input, {
        baseURL: this.gatewayURL,
        signal: options?.signal,
        timeout: options?.timeout,
      });
    } catch (error) {
      if (error instanceof SDKError) {
        throw error;
      }
      // The message used to be passed as the `code`, so every wrapped failure
      // arrived with a code like "fetch failed" and no usable classification.
      throw new SDKError(
        `function ${functionName} failed: ${
          error instanceof Error ? error.message : String(error)
        }`,
        500,
        "FUNCTION_INVOKE_FAILED",
        { function: functionName, namespace: this.namespace }
      );
    }
  }
}
