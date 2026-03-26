export interface AuthConfig {
  apiKey?: string;
  jwt?: string;
}

export interface WhoAmI {
  address?: string;
  namespace?: string;
  authenticated: boolean;
}

export interface StorageAdapter {
  get(key: string): Promise<string | null>;
  set(key: string, value: string): Promise<void>;
  clear(): Promise<void>;
}

export class MemoryStorage implements StorageAdapter {
  private storage: Map<string, string> = new Map();

  async get(key: string): Promise<string | null> {
    return this.storage.get(key) ?? null;
  }

  async set(key: string, value: string): Promise<void> {
    this.storage.set(key, value);
  }

  async clear(): Promise<void> {
    this.storage.clear();
  }
}

export class LocalStorageAdapter implements StorageAdapter {
  private prefix = "@network/sdk:";

  async get(key: string): Promise<string | null> {
    if (typeof globalThis !== "undefined" && globalThis.localStorage) {
      return globalThis.localStorage.getItem(this.prefix + key);
    }
    return null;
  }

  async set(key: string, value: string): Promise<void> {
    if (typeof globalThis !== "undefined" && globalThis.localStorage) {
      globalThis.localStorage.setItem(this.prefix + key, value);
    }
  }

  async clear(): Promise<void> {
    if (typeof globalThis !== "undefined" && globalThis.localStorage) {
      const keysToDelete: string[] = [];
      for (let i = 0; i < globalThis.localStorage.length; i++) {
        const key = globalThis.localStorage.key(i);
        if (key?.startsWith(this.prefix)) {
          keysToDelete.push(key);
        }
      }
      keysToDelete.forEach((key) => globalThis.localStorage.removeItem(key));
    }
  }
}
