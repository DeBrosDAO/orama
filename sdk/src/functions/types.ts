/**
 * Serverless Functions Types
 * Type definitions for calling serverless functions on the Orama Network
 */

/**
 * Generic response from a serverless function
 */
export interface FunctionResponse<T = unknown> {
  success: boolean;
  error?: string;
  data?: T;
}

/**
 * Standard success/error response used by many functions
 */
export interface SuccessResponse {
  success: boolean;
  error?: string;
}
