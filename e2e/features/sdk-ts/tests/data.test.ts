// Data-plane methods the SDK's own e2e suite does not reach: storage
// getBinary, db dropTable and the Repository, functions.invoke of a function
// that does not exist.
import { NotFoundError, SDKError } from "../../../../sdk/src/index";
import { onFleet, ownerClient, unique } from "./fleet";

describe.skipIf(!onFleet)("sdk data plane", () => {
  it("storage.getBinary streams back exactly the uploaded bytes", async () => {
    const client = ownerClient();
    const bytes = new Uint8Array(200 * 1024);
    for (let i = 0; i < bytes.length; i++) bytes[i] = (i * 31 + 7) & 0xff;
    const up = await client.storage.upload(bytes, "e2e-binary.bin");
    try {
      const response = await client.storage.getBinary(up.cid);
      expect(response.ok).toBe(true);
      const got = new Uint8Array(await response.arrayBuffer());
      expect(got.length).toBe(bytes.length);
      expect(Buffer.compare(Buffer.from(got), Buffer.from(bytes))).toBe(0);
    } finally {
      await client.storage.unpin(up.cid);
    }
  });

  it("the Repository saves, finds, updates and removes rows, and dropTable removes the table", async () => {
    const client = ownerClient();
    const table = unique("e2e_repo");
    await client.db.createTable(`CREATE TABLE ${table} (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT, n INTEGER)`);
    let dropped = false;
    try {
      const repo = client.db.repository<{ id?: number; name: string; n: number }>(table);
      const saved = await repo.save({ name: "O'Brien; DROP TABLE x; --", n: 1 });
      expect(saved.id).toBeGreaterThan(0);
      await repo.save({ name: "‮unicodé", n: 2 });
      expect((await repo.find({})).length).toBe(2);
      const one = await repo.findOne({ id: saved.id });
      expect(one?.name).toBe("O'Brien; DROP TABLE x; --");
      await repo.save({ ...saved, n: 42 });
      expect((await repo.findOne({ id: saved.id }))?.n).toBe(42);
      await repo.remove(saved);
      expect(await repo.findOne({ id: saved.id })).toBeNull();
      await client.db.dropTable(table);
      dropped = true;
      const gone = await client.db.query(`SELECT * FROM ${table}`).catch((e) => e);
      expect(gone).toBeInstanceOf(SDKError);
      // A statement SQLite rejects is INTERNAL (a 500) whatever it names: the
      // error says the table is gone, the status class does not.
      expect((gone as SDKError).httpStatus).toBeGreaterThanOrEqual(400);
    } finally {
      if (!dropped) await client.db.dropTable(table);
    }
  });

  it("the Repository refuses a table or key name that is not an identifier, before any request", () => {
    const client = ownerClient();
    expect(() => client.db.repository("users; DROP TABLE users")).toThrow(SDKError);
    expect(() => client.db.repository("users", "id--")).toThrow(SDKError);
  });

  it("dropTable of a table that does not exist is an error", async () => {
    const err = await ownerClient().db.dropTable(unique("e2e_absent")).catch((e) => e);
    expect(err).toBeInstanceOf(SDKError);
    expect((err as SDKError).httpStatus).toBeGreaterThanOrEqual(400);
    expect((err as SDKError).httpStatus).toBeLessThan(500);
  });

  it("functions.invoke of a function that does not exist is a typed not-found", async () => {
    const err = await ownerClient().functions.invoke("e2e-absent-fn", { hello: "world" }).catch((e) => e);
    expect(err).toBeInstanceOf(SDKError);
    expect(err).toBeInstanceOf(NotFoundError);
    expect((err as SDKError).httpStatus).toBe(404);
  });
});
