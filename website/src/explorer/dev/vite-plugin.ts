import type { IncomingMessage, ServerResponse } from "node:http";
import type { Plugin, ViteDevServer } from "vite";

/**
 * The explorer's dev gateway: `ORAMA_EXPLORER_DEV_GATEWAY=1 pnpm dev` serves a fixed, invented chain at
 * /v1/chain/ on the dev server, so every explorer page can be opened without a network. Read it with the
 * custom gateway of the explorer: /explorer/custom?gateway=http://localhost:5173 (the explorer sends no
 * credentials to it and asks the reader to confirm a gateway the registry does not list).
 *
 * Which host the page names picks the behaviour: http://localhost:<port> is a node with the chain indexer,
 * http://127.0.0.1:<port> is a node without one (every /index/ route answers 503 "runs no chain indexer").
 *
 * This is a development tool and nothing else: the plugin applies to `vite dev` only (`apply: "serve"`), it
 * is added to the config only when the environment variable is set, it refuses to start in production mode,
 * and nothing under src/ imports src/explorer/dev, so the production bundle cannot contain it
 * (dev-gateway.test.ts builds the explorer page and checks).
 */

export const DEV_GATEWAY_ENV = "ORAMA_EXPLORER_DEV_GATEWAY";
const CHAIN_PREFIX = "/v1/chain/";
const DEV_MODULE = "/src/explorer/dev/dev-gateway.ts";
/** How often the dev stream announces a block, so the live head can be watched moving. */
const HEAD_EVERY_MS = 4_000;
const BLOCK_SECONDS = 2;
const NO_INDEXER_BODY = { error: { code: "NO_INDEXER", message: "this node runs no chain indexer" } };
const LOCAL_NO_INDEXER_HOST = "127.0.0.1";

interface DevModule {
  devAnswer(path: string): Promise<{ status: number; body: unknown }>;
  devHead(): { height: number; time: string; chain_id: string };
}

function withoutIndexer(req: IncomingMessage): boolean {
  return (req.headers.host ?? "").startsWith(`${LOCAL_NO_INDEXER_HOST}:`);
}

function sendJson(res: ServerResponse, status: number, body: unknown): void {
  res.statusCode = status;
  res.setHeader("Content-Type", "application/json");
  res.setHeader("Cache-Control", "no-store");
  res.end(JSON.stringify(body));
}

/** What a reader of the dev gateway is told when a read failed inside it: a fixed line, like the real gateway's. */
export const DEV_FAILURE_BODY = { error: { message: "dev gateway failed; see the dev server's console" } };

/** Answers a read that failed with the fixed body and writes what failed to the dev server's console, not to the client. */
export function failed(res: ServerResponse, err: unknown, log: (line: string) => void): void {
  log(`[explorer-dev-gateway] a read failed: ${err instanceof Error ? (err.stack ?? err.message) : String(err)}`);
  sendJson(res, 500, DEV_FAILURE_BODY);
}

function streamHeads(dev: DevModule, req: IncomingMessage, res: ServerResponse): void {
  res.writeHead(200, { "Content-Type": "text/event-stream", "Cache-Control": "no-store", Connection: "keep-alive" });
  let height = dev.devHead().height;
  const send = () => {
    const head = dev.devHead();
    const time = new Date(Date.parse(head.time) + (height - head.height) * BLOCK_SECONDS * 1000).toISOString();
    res.write(`event: head\ndata: ${JSON.stringify({ ...head, height, time })}\n\n`);
  };
  send();
  const timer = setInterval(() => {
    height += 1;
    send();
  }, HEAD_EVERY_MS);
  req.on("close", () => clearInterval(timer));
}

async function serve(server: ViteDevServer, req: IncomingMessage, res: ServerResponse): Promise<void> {
  const dev = (await server.ssrLoadModule(DEV_MODULE)) as DevModule;
  const path = (req.url ?? "").slice(CHAIN_PREFIX.length);
  if (path === "head/stream") return streamHeads(dev, req, res);
  if (path.startsWith("index/") && withoutIndexer(req)) return sendJson(res, 503, NO_INDEXER_BODY);
  const answer = await dev.devAnswer(path);
  sendJson(res, answer.status, answer.body);
}

/** The dev gateway as a Vite plugin; a no-op unless the environment variable is "1". Never part of a build. */
export function explorerDevGatewayPlugin(env: Record<string, string | undefined> = process.env): Plugin[] {
  if (env[DEV_GATEWAY_ENV] !== "1") return [];
  return [
    {
      name: "explorer-dev-gateway",
      apply: "serve",
      configureServer(server) {
        if (server.config.mode === "production") {
          throw new Error(`${DEV_GATEWAY_ENV} cannot be used in production mode`);
        }
        server.middlewares.use((req, res, next) => {
          if (!(req.url ?? "").startsWith(CHAIN_PREFIX)) return next();
          serve(server, req, res).catch((err: unknown) => failed(res, err, (line) => server.config.logger.error(line)));
        });
      },
    },
  ];
}
