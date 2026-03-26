import { describe, it, expect, beforeEach } from "vitest";
import { createTestClient, skipIfNoGateway } from "./setup";

describe("Cache", () => {
  if (skipIfNoGateway()) {
    console.log("Skipping cache tests - gateway not available");
    return;
  }

  const testDMap = "test-cache";

  beforeEach(async () => {
    // Clean up test keys before each test
    const client = await createTestClient();
    try {
      const keys = await client.cache.scan(testDMap);
      for (const key of keys.keys) {
        await client.cache.delete(testDMap, key);
      }
    } catch (err) {
      // Ignore errors during cleanup
    }
  }, 30000); // 30 second timeout for slow SCAN operations

  it("should check cache health", async () => {
    const client = await createTestClient();
    const health = await client.cache.health();
    expect(health.status).toBe("ok");
    expect(health.service).toBe("olric");
  });

  it("should put and get a value", async () => {
    const client = await createTestClient();
    const testKey = "test-key-1";
    const testValue = "test-value-1";

    // Put value
    const putResult = await client.cache.put(testDMap, testKey, testValue);
    expect(putResult.status).toBe("ok");
    expect(putResult.key).toBe(testKey);
    expect(putResult.dmap).toBe(testDMap);

    // Get value
    const getResult = await client.cache.get(testDMap, testKey);
    expect(getResult).not.toBeNull();
    expect(getResult!.key).toBe(testKey);
    expect(getResult!.value).toBe(testValue);
    expect(getResult!.dmap).toBe(testDMap);
  });

  it("should put and get complex objects", async () => {
    const client = await createTestClient();
    const testKey = "test-key-2";
    const testValue = {
      name: "John",
      age: 30,
      tags: ["developer", "golang"],
    };

    // Put object
    await client.cache.put(testDMap, testKey, testValue);

    // Get object
    const getResult = await client.cache.get(testDMap, testKey);
    expect(getResult).not.toBeNull();
    expect(getResult!.value).toBeDefined();
    expect(getResult!.value.name).toBe(testValue.name);
    expect(getResult!.value.age).toBe(testValue.age);
  });

  it("should put value with TTL", async () => {
    const client = await createTestClient();
    const testKey = "test-key-ttl";
    const testValue = "ttl-value";

    // Put with TTL
    const putResult = await client.cache.put(
      testDMap,
      testKey,
      testValue,
      "5m"
    );
    expect(putResult.status).toBe("ok");

    // Verify value exists
    const getResult = await client.cache.get(testDMap, testKey);
    expect(getResult).not.toBeNull();
    expect(getResult!.value).toBe(testValue);
  });

  it("should delete a value", async () => {
    const client = await createTestClient();
    const testKey = "test-key-delete";
    const testValue = "delete-me";

    // Put value
    await client.cache.put(testDMap, testKey, testValue);

    // Verify it exists
    const before = await client.cache.get(testDMap, testKey);
    expect(before).not.toBeNull();
    expect(before!.value).toBe(testValue);

    // Delete value
    const deleteResult = await client.cache.delete(testDMap, testKey);
    expect(deleteResult.status).toBe("ok");
    expect(deleteResult.key).toBe(testKey);

    // Verify it's deleted (should return null, not throw)
    const after = await client.cache.get(testDMap, testKey);
    expect(after).toBeNull();
  });

  it("should scan keys", async () => {
    const client = await createTestClient();

    // Put multiple keys
    await client.cache.put(testDMap, "key-1", "value-1");
    await client.cache.put(testDMap, "key-2", "value-2");
    await client.cache.put(testDMap, "key-3", "value-3");

    // Scan all keys
    const scanResult = await client.cache.scan(testDMap);
    expect(scanResult.count).toBeGreaterThanOrEqual(3);
    expect(scanResult.keys).toContain("key-1");
    expect(scanResult.keys).toContain("key-2");
    expect(scanResult.keys).toContain("key-3");
    expect(scanResult.dmap).toBe(testDMap);
  });

  it("should scan keys with regex match", async () => {
    const client = await createTestClient();

    // Put keys with different patterns
    await client.cache.put(testDMap, "user-1", "value-1");
    await client.cache.put(testDMap, "user-2", "value-2");
    await client.cache.put(testDMap, "session-1", "value-3");

    // Scan with regex match
    const scanResult = await client.cache.scan(testDMap, "^user-");
    expect(scanResult.count).toBeGreaterThanOrEqual(2);
    expect(scanResult.keys).toContain("user-1");
    expect(scanResult.keys).toContain("user-2");
    expect(scanResult.keys).not.toContain("session-1");
  });

  it("should handle non-existent key gracefully", async () => {
    const client = await createTestClient();
    const nonExistentKey = "non-existent-key";

    // Cache misses should return null, not throw an error
    const result = await client.cache.get(testDMap, nonExistentKey);
    expect(result).toBeNull();
  });

  it("should handle empty dmap name", async () => {
    const client = await createTestClient();

    try {
      await client.cache.get("", "test-key");
      expect.fail("Expected get to fail with empty dmap");
    } catch (err: any) {
      expect(err.message).toBeDefined();
    }
  });
});
