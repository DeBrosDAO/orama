import WebSocket from "isomorphic-ws";

export class GatewayClient {
  constructor(
    private baseUrl: string,
    private apiKey: string,
    private http = fetch
  ) {}

  private headers(json = true): Record<string, string> {
    const h: Record<string, string> = { "X-API-Key": this.apiKey };
    if (json) h["Content-Type"] = "application/json";
    return h;
  }

  // Database
  async createTable(schema: string): Promise<void> {
    const r = await this.http(`${this.baseUrl}/v1/rqlite/create-table`, {
      method: "POST",
      headers: this.headers(),
      body: JSON.stringify({ schema }),
    });
    if (!r.ok) throw new Error(`createTable failed: ${r.status}`);
  }

  async dropTable(table: string): Promise<void> {
    const r = await this.http(`${this.baseUrl}/v1/rqlite/drop-table`, {
      method: "POST",
      headers: this.headers(),
      body: JSON.stringify({ table }),
    });
    if (!r.ok) throw new Error(`dropTable failed: ${r.status}`);
  }

  async query<T = any>(sql: string, args: any[] = []): Promise<{ rows: T[] }> {
    const r = await this.http(`${this.baseUrl}/v1/rqlite/query`, {
      method: "POST",
      headers: this.headers(),
      body: JSON.stringify({ sql, args }),
    });
    if (!r.ok) throw new Error(`query failed: ${r.status}`);
    return r.json();
  }

  async transaction(statements: string[]): Promise<void> {
    const r = await this.http(`${this.baseUrl}/v1/rqlite/transaction`, {
      method: "POST",
      headers: this.headers(),
      body: JSON.stringify({ statements }),
    });
    if (!r.ok) throw new Error(`transaction failed: ${r.status}`);
  }

  async schema(): Promise<any> {
    const r = await this.http(`${this.baseUrl}/v1/rqlite/schema`, {
      headers: this.headers(false),
    });
    if (!r.ok) throw new Error(`schema failed: ${r.status}`);
    return r.json();
  }

  // Storage
  async put(key: string, value: Uint8Array | string): Promise<void> {
    const body =
      typeof value === "string" ? new TextEncoder().encode(value) : value;
    const r = await this.http(
      `${this.baseUrl}/v1/storage/put?key=${encodeURIComponent(key)}`,
      {
        method: "POST",
        headers: { "X-API-Key": this.apiKey },
        body,
      }
    );
    if (!r.ok) throw new Error(`put failed: ${r.status}`);
  }

  async get(key: string): Promise<Uint8Array> {
    const r = await this.http(
      `${this.baseUrl}/v1/storage/get?key=${encodeURIComponent(key)}`,
      {
        headers: { "X-API-Key": this.apiKey },
      }
    );
    if (!r.ok) throw new Error(`get failed: ${r.status}`);
    const buf = new Uint8Array(await r.arrayBuffer());
    return buf;
  }

  async exists(key: string): Promise<boolean> {
    const r = await this.http(
      `${this.baseUrl}/v1/storage/exists?key=${encodeURIComponent(key)}`,
      {
        headers: this.headers(false),
      }
    );
    if (!r.ok) throw new Error(`exists failed: ${r.status}`);
    const j = await r.json();
    return !!j.exists;
  }

  async list(prefix = ""): Promise<string[]> {
    const r = await this.http(
      `${this.baseUrl}/v1/storage/list?prefix=${encodeURIComponent(prefix)}`,
      {
        headers: this.headers(false),
      }
    );
    if (!r.ok) throw new Error(`list failed: ${r.status}`);
    const j = await r.json();
    return j.keys || [];
  }

  async delete(key: string): Promise<void> {
    const r = await this.http(`${this.baseUrl}/v1/storage/delete`, {
      method: "POST",
      headers: this.headers(),
      body: JSON.stringify({ key }),
    });
    if (!r.ok) throw new Error(`delete failed: ${r.status}`);
  }

  // PubSub (minimal)
  subscribe(
    topic: string,
    onMessage: (data: Uint8Array) => void
  ): { close: () => void } {
    const url = new URL(`${this.baseUrl.replace(/^http/, "ws")}/v1/pubsub/ws`);
    url.searchParams.set("topic", topic);
    const ws = new WebSocket(url.toString(), {
      headers: { "X-API-Key": this.apiKey },
    } as any);
    ws.binaryType = "arraybuffer";
    ws.onmessage = (ev: any) => {
      const data =
        ev.data instanceof ArrayBuffer
          ? new Uint8Array(ev.data)
          : new TextEncoder().encode(String(ev.data));
      onMessage(data);
    };
    return { close: () => ws.close() };
  }

  async publish(topic: string, data: Uint8Array | string): Promise<void> {
    const bytes =
      typeof data === "string" ? new TextEncoder().encode(data) : data;
    const b64 = Buffer.from(bytes).toString("base64");
    const r = await this.http(`${this.baseUrl}/v1/pubsub/publish`, {
      method: "POST",
      headers: this.headers(),
      body: JSON.stringify({ topic, data_base64: b64 }),
    });
    if (!r.ok) throw new Error(`publish failed: ${r.status}`);
  }
}
