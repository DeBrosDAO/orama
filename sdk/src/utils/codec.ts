/**
 * Base64 Codec for cross-platform encoding/decoding
 * Works in both Node.js and browser environments
 */
export class Base64Codec {
  /**
   * Encode string or Uint8Array to base64
   */
  static encode(input: string | Uint8Array): string {
    if (typeof input === "string") {
      return this.encodeString(input);
    }
    return this.encodeBytes(input);
  }

  /**
   * Encode string to base64
   */
  static encodeString(str: string): string {
    if (this.isNode()) {
      return Buffer.from(str).toString("base64");
    }
    // Browser
    return btoa(
      encodeURIComponent(str).replace(/%([0-9A-F]{2})/g, (match, p1) =>
        String.fromCharCode(parseInt(p1, 16))
      )
    );
  }

  /**
   * Encode Uint8Array to base64
   */
  static encodeBytes(bytes: Uint8Array): string {
    if (this.isNode()) {
      return Buffer.from(bytes).toString("base64");
    }
    // Browser
    let binary = "";
    for (let i = 0; i < bytes.length; i++) {
      binary += String.fromCharCode(bytes[i]);
    }
    return btoa(binary);
  }

  /**
   * Decode base64 to string
   */
  static decode(b64: string): string {
    if (this.isNode()) {
      return Buffer.from(b64, "base64").toString("utf-8");
    }
    // Browser
    const binary = atob(b64);
    const bytes = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i++) {
      bytes[i] = binary.charCodeAt(i);
    }
    return new TextDecoder().decode(bytes);
  }

  /**
   * Check if running in Node.js environment
   */
  private static isNode(): boolean {
    return typeof Buffer !== "undefined";
  }
}
