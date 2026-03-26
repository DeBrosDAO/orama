/**
 * Platform detection utilities
 * Helps determine runtime environment (Node.js vs Browser)
 */
export const Platform = {
  /**
   * Check if running in Node.js
   */
  isNode: (): boolean => {
    return typeof process !== "undefined" && !!process.versions?.node;
  },

  /**
   * Check if running in browser
   */
  isBrowser: (): boolean => {
    return typeof window !== "undefined";
  },

  /**
   * Check if localStorage is available
   */
  hasLocalStorage: (): boolean => {
    try {
      return typeof localStorage !== "undefined" && localStorage !== null;
    } catch {
      return false;
    }
  },

  /**
   * Check if Buffer is available (Node.js)
   */
  hasBuffer: (): boolean => {
    return typeof Buffer !== "undefined";
  },

  /**
   * Check if btoa/atob are available (Browser)
   */
  hasBase64: (): boolean => {
    return typeof btoa !== "undefined" && typeof atob !== "undefined";
  },
};
