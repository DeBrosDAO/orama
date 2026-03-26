import { describe, it, expect, beforeAll } from "vitest";
import { createTestClient, skipIfNoGateway } from "./setup";

describe("Storage", () => {
  beforeAll(() => {
    if (skipIfNoGateway()) {
      console.log("Skipping storage tests");
    }
  });

  it("should upload a file", async () => {
    const client = await createTestClient();
    const testContent = "Hello, IPFS!";
    const testFile = new File([testContent], "test.txt", {
      type: "text/plain",
    });

    const result = await client.storage.upload(testFile);

    expect(result).toBeDefined();
    expect(result.cid).toBeDefined();
    expect(typeof result.cid).toBe("string");
    expect(result.cid.length).toBeGreaterThan(0);
    expect(result.name).toBe("test.txt");
    expect(result.size).toBeGreaterThan(0);
  });

  it("should upload a Blob", async () => {
    const client = await createTestClient();
    const testContent = "Test blob content";
    const blob = new Blob([testContent], { type: "text/plain" });

    const result = await client.storage.upload(blob, "blob.txt");

    expect(result).toBeDefined();
    expect(result.cid).toBeDefined();
    expect(typeof result.cid).toBe("string");
    expect(result.name).toBe("blob.txt");
  });

  it("should upload ArrayBuffer", async () => {
    const client = await createTestClient();
    const testContent = "Test array buffer";
    const buffer = new TextEncoder().encode(testContent).buffer;

    const result = await client.storage.upload(buffer, "buffer.bin");

    expect(result).toBeDefined();
    expect(result.cid).toBeDefined();
    expect(typeof result.cid).toBe("string");
  });

  it("should upload Uint8Array", async () => {
    const client = await createTestClient();
    const testContent = "Test uint8array";
    const uint8Array = new TextEncoder().encode(testContent);

    const result = await client.storage.upload(uint8Array, "uint8.txt");

    expect(result).toBeDefined();
    expect(result.cid).toBeDefined();
    expect(typeof result.cid).toBe("string");
  });

  it("should pin a CID", async () => {
    const client = await createTestClient();
    // First upload a file to get a CID
    const testContent = "File to pin";
    const testFile = new File([testContent], "pin-test.txt", {
      type: "text/plain",
    });

    const uploadResult = await client.storage.upload(testFile);
    const cid = uploadResult.cid;

    // Now pin it
    const pinResult = await client.storage.pin(cid, "pinned-file");

    expect(pinResult).toBeDefined();
    expect(pinResult.cid).toBe(cid);
    expect(pinResult.name).toBe("pinned-file");
  });

  it("should get pin status", async () => {
    const client = await createTestClient();
    // First upload and pin a file
    const testContent = "File for status check";
    const testFile = new File([testContent], "status-test", {
      type: "text/plain",
    });

    const uploadResult = await client.storage.upload(testFile);
    await client.storage.pin(uploadResult.cid, "status-test");

    // Wait a bit for pin to propagate
    await new Promise((resolve) => setTimeout(resolve, 1000));

    const status = await client.storage.status(uploadResult.cid);

    expect(status).toBeDefined();
    expect(status.cid).toBe(uploadResult.cid);
    expect(status.name).toBe("status-test");
    expect(status.status).toBeDefined();
    expect(typeof status.status).toBe("string");
    expect(status.replication_factor).toBeGreaterThanOrEqual(0);
    expect(Array.isArray(status.peers)).toBe(true);
  });

  it("should retrieve content by CID", async () => {
    const client = await createTestClient();
    const testContent = "Content to retrieve";
    const testFile = new File([testContent], "retrieve-test.txt", {
      type: "text/plain",
    });

    const uploadResult = await client.storage.upload(testFile);
    const cid = uploadResult.cid;

    // Wait for IPFS replication across nodes (30 seconds)
    await new Promise((resolve) => setTimeout(resolve, 2000));

    // Get the content back
    const stream = await client.storage.get(cid);

    expect(stream).toBeDefined();
    expect(stream instanceof ReadableStream).toBe(true);

    // Read the stream
    const reader = stream.getReader();
    const chunks: Uint8Array[] = [];
    let done = false;

    while (!done) {
      const { value, done: streamDone } = await reader.read();
      done = streamDone;
      if (value) {
        chunks.push(value);
      }
    }

    // Combine chunks
    const totalLength = chunks.reduce((sum, chunk) => sum + chunk.length, 0);
    const combined = new Uint8Array(totalLength);
    let offset = 0;
    for (const chunk of chunks) {
      combined.set(chunk, offset);
      offset += chunk.length;
    }

    const retrievedContent = new TextDecoder().decode(combined);
    expect(retrievedContent).toBe(testContent);
  });

  it("should unpin a CID", async () => {
    const client = await createTestClient();
    // First upload and pin a file
    const testContent = "File to unpin";
    const testFile = new File([testContent], "unpin-test.txt", {
      type: "text/plain",
    });

    const uploadResult = await client.storage.upload(testFile);
    await client.storage.pin(uploadResult.cid, "unpin-test");

    // Wait a bit
    await new Promise((resolve) => setTimeout(resolve, 1000));

    // Unpin it
    await expect(client.storage.unpin(uploadResult.cid)).resolves.not.toThrow();
  });

  it("should handle upload errors gracefully", async () => {
    const client = await createTestClient();
    // Try to upload invalid data
    const invalidFile = null as any;

    await expect(client.storage.upload(invalidFile)).rejects.toThrow();
  });

  it("should handle status errors for non-existent CID", async () => {
    const client = await createTestClient();
    const fakeCID = "QmInvalidCID123456789";

    await expect(client.storage.status(fakeCID)).rejects.toThrow();
  });

  it("should upload large content", async () => {
    const client = await createTestClient();
    // Create a larger file (100KB)
    const largeContent = "x".repeat(100 * 1024);
    const largeFile = new File([largeContent], "large.txt", {
      type: "text/plain",
    });

    const result = await client.storage.upload(largeFile);

    expect(result).toBeDefined();
    expect(result.cid).toBeDefined();
    expect(result.size).toBeGreaterThanOrEqual(100 * 1024);
  });

  it("should upload binary content", async () => {
    const client = await createTestClient();
    // Create binary data
    const binaryData = new Uint8Array([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a]); // PNG header
    const blob = new Blob([binaryData], { type: "image/png" });

    const result = await client.storage.upload(blob, "image.png");

    expect(result).toBeDefined();
    expect(result.cid).toBeDefined();
    expect(result.name).toBe("image.png");
  });
});
