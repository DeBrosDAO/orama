import type { ReactNode } from "react";
import { useEffect, useState } from "react";
import { Link, useParams, useSearchParams } from "react-router";
import {
  getBlock,
  getBlockMetas,
  getPool,
  getStatus,
  getSupply,
  getTx,
  getValidators,
  type ChainBlock,
  type ChainBlockMeta,
  type ChainStatus,
  type ChainValidator,
  type Pool,
  type Supply,
  type ValidatorSet,
} from "./api";
import { formatBig, formatNorama, sharePct, shareWidth, short } from "./format";
import { bech32Hrp, classify } from "./search";
import { ExplorerFrame, Kicker, Panel, Stat } from "./ui";

const HOME_REFRESH_MS = 8_000;

interface ChainState<T> {
  phase: "idle" | "loading" | "ready" | "error";
  key: string;
  data: T | null;
  error: string | null;
}

function useChain<T>(key: string, load: (key: string) => Promise<T>, refresh = ""): ChainState<T> {
  const [state, setState] = useState<ChainState<T>>({
    phase: key ? "loading" : "idle",
    key,
    data: null,
    error: null,
  });
  useEffect(() => {
    if (!key) {
      setState({ phase: "idle", key: "", data: null, error: null });
      return;
    }
    let live = true;
    setState((s) => ({
      phase: "loading",
      key,
      data: s.key === key ? s.data : null,
      error: null,
    }));
    load(key).then(
      (data) => {
        if (live) setState({ phase: "ready", key, data, error: null });
      },
      (err: unknown) => {
        if (!live) return;
        const message = err instanceof Error && err.message ? err.message : "The chain request failed.";
        setState((s) => ({
          phase: "error",
          key,
          data: s.key === key ? s.data : null,
          error: message,
        }));
      },
    );
    return () => {
      live = false;
    };
  }, [key, refresh, load]);
  return state.key === key ? state : { phase: "loading", key, data: null, error: null };
}

function useTick(ms: number): string {
  const [n, setN] = useState(0);
  useEffect(() => {
    const id = window.setInterval(() => {
      if (document.visibilityState !== "hidden") setN((v) => v + 1);
    }, ms);
    const onVis = () => {
      if (document.visibilityState === "visible") setN((v) => v + 1);
    };
    document.addEventListener("visibilitychange", onVis);
    return () => {
      window.clearInterval(id);
      document.removeEventListener("visibilitychange", onVis);
    };
  }, [ms]);
  return String(n);
}

function Loading() {
  return (
    <ExplorerFrame>
      <p className="text-sm text-muted">Loading the chain.</p>
    </ExplorerFrame>
  );
}

function Failed({ title, message }: { title: string; message: string }) {
  return (
    <ExplorerFrame>
      <Kicker>Chain</Kicker>
      <h1 className="font-display text-3xl mt-1">{title}</h1>
      <p className="mt-4 max-w-xl break-all text-sm text-muted">{message}</p>
      <Link to="/explorer" className="mt-4 inline-block text-sm text-muted underline">
        Back
      </Link>
    </ExplorerFrame>
  );
}

function Banner({ message }: { message: string | null }) {
  if (!message) return null;
  return (
    <p className="mb-4 rounded-xl border border-white/[0.08] bg-white/[0.03] px-4 py-3 break-all text-sm text-muted">
      {message}
    </p>
  );
}

function QueryBody<T>({
  state,
  invalid,
  children,
}: {
  state: ChainState<T>;
  invalid?: string;
  children: (data: T, error: string | null) => ReactNode;
}) {
  if (invalid) return <Failed title="Not a chain query" message={invalid} />;
  if (!state.data) {
    if (state.phase === "error") {
      return <Failed title="The chain did not answer" message={state.error ?? "The chain request failed."} />;
    }
    if (state.phase === "idle") return <Failed title="Not a chain query" message="That query is not valid." />;
    return <Loading />;
  }
  return children(state.data, state.phase === "error" ? state.error : null);
}

function heightKey(height: string | undefined): string {
  return height && /^[1-9][0-9]{0,15}$/.test(height) ? height : "";
}

function txKey(hash: string | undefined): string {
  if (!hash) return "";
  const bare = hash.replace(/^0x/i, "");
  return /^[0-9a-fA-F]{64}$/.test(bare) ? bare.toUpperCase() : "";
}

function accountKey(address: string | undefined): string {
  if (!address || bech32Hrp(address) !== "orama") return "";
  return address.toLowerCase();
}

function validatorKey(address: string | undefined): string {
  if (!address) return "";
  if (/^[0-9a-fA-F]{40}$/.test(address)) return address.toUpperCase();
  const hrp = bech32Hrp(address);
  if (hrp === "oramavaloper" || hrp === "oramavalcons") return address.toLowerCase();
  return "";
}

function txCount(n: number): string {
  return n === 1 ? "1 tx" : `${n.toLocaleString("en-US")} txs`;
}

function when(iso: string): string {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return iso;
  return new Date(t).toLocaleString("en-US", { hour12: false });
}

function clip(s: string, n: number): string {
  return s.length > n ? `${s.slice(0, n)}…` : s;
}

interface HomeData {
  status: ChainStatus;
  metas: ChainBlockMeta[];
  head: ChainBlock | null;
  validators: ValidatorSet;
}

async function loadHome(_key: string): Promise<HomeData> {
  const status = await getStatus();
  if (status.height < 1) {
    return { status, metas: [], head: null, validators: await getValidators() };
  }
  const min = Math.max(1, status.height - 7);
  const [metas, head, validators] = await Promise.all([
    getBlockMetas(min, status.height),
    getBlock(status.height),
    getValidators(),
  ]);
  return { status, metas, head, validators };
}

function loadBlock(height: string): Promise<ChainBlock> {
  return getBlock(Number(height));
}

interface SupplyData {
  supply: Supply;
  pool: Pool;
}

async function loadSupply(_key: string): Promise<SupplyData> {
  const [supply, pool] = await Promise.all([getSupply(), getPool()]);
  return { supply, pool };
}

async function loadAccount(address: string): Promise<{ address: string; status: ChainStatus }> {
  return { address, status: await getStatus() };
}

interface ValidatorData {
  address: string;
  set: ValidatorSet;
  match: ChainValidator | null;
}

async function loadValidator(address: string): Promise<ValidatorData> {
  const set = await getValidators();
  const match = set.validators.find((v) => v.address.toUpperCase() === address.toUpperCase()) ?? null;
  return { address, set, match };
}

type SearchData =
  | { kind: "none" }
  | { kind: "shielded" }
  | { kind: "block"; height: number }
  | { kind: "tx"; hash: string; height: number }
  | { kind: "account"; address: string; chainId: string }
  | { kind: "validator"; address: string; found: boolean; note: string };

async function loadSearch(q: string): Promise<SearchData> {
  const hit = classify(q);
  if (!hit) return { kind: "none" };
  switch (hit.kind) {
    case "shielded-address":
      return { kind: "shielded" };
    case "block": {
      const block = await getBlock(hit.height);
      return { kind: "block", height: block.height };
    }
    case "tx": {
      const tx = await getTx(hit.hash);
      return { kind: "tx", hash: tx.hash, height: tx.height };
    }
    case "account": {
      const status = await getStatus();
      return { kind: "account", address: hit.address, chainId: status.chainId };
    }
    case "validator": {
      const set = await getValidators();
      const match = set.validators.find((v) => v.address.toUpperCase() === hit.address.toUpperCase());
      const note = match
        ? ""
        : bech32Hrp(hit.address)
          ? "CometBFT's validator query returns consensus addresses, not operator addresses."
          : "That address is not in the current CometBFT validator set.";
      return { kind: "validator", address: match?.address ?? hit.address, found: Boolean(match), note };
    }
  }
}

export function Home() {
  const tick = useTick(HOME_REFRESH_MS);
  const state = useChain("home", loadHome, tick);
  return (
    <QueryBody state={state}>
      {(data, error) => <HomeView data={data} error={error} />}
    </QueryBody>
  );
}

function HomeView({ data, error }: { data: HomeData; error: string | null }) {
  const { status, metas, head, validators } = data;
  const latest = head?.txHashes.slice(0, 8) ?? [];
  return (
    <ExplorerFrame>
      <div className="flex h-full min-h-[640px] flex-col gap-4">
        <Banner message={error} />
        <Panel className="grid grid-cols-2 divide-y divide-white/[0.06] lg:grid-cols-4 lg:divide-x lg:divide-y-0">
          {status.height >= 1 ? (
            <Link to={`/explorer/block/${status.height}`} className="px-5 py-4 hover:bg-white/[0.02]">
              <Stat
                label="Latest block"
                value={status.height.toLocaleString("en-US")}
                hint={status.catchingUp ? `Catching up · ${status.chainId}` : `Committed · ${status.chainId}`}
              />
            </Link>
          ) : (
            <div className="px-5 py-4">
              <Stat label="Latest block" value="0" hint={status.chainId} />
            </div>
          )}
          <Link to="/explorer/epoch" className="px-5 py-4 hover:bg-white/[0.02]">
            <Stat label="Epoch" value="—" hint="No emission query on this build." />
          </Link>
          <div className="px-5 py-4">
            <Stat label="Base fee" value="—" hint="No fees query on this build." />
          </div>
          <Link to="/explorer/committee" className="px-5 py-4 hover:bg-white/[0.02]">
            <Stat label="Hand-over" value="—" hint="No power query on this build." />
          </Link>
        </Panel>

        <Panel className="px-5 py-4">
          <div className="mb-3 flex items-center justify-between">
            <Kicker>Arriving now</Kicker>
            {error ? null : (
              <span className="flex items-center gap-2 font-mono text-[10px] uppercase tracking-[0.16em] text-muted">
                <span className="h-1.5 w-1.5 rounded-full bg-emerald-400" />
                Live
              </span>
            )}
          </div>
          {metas.length === 0 ? <p className="text-sm text-muted">No blocks in this range.</p> : null}
          <div className="grid grid-cols-4 gap-2 sm:grid-cols-8">
            {metas.map((meta) => (
              <Link
                key={meta.height}
                to={`/explorer/block/${meta.height}`}
                className={`rounded-xl px-2 py-3 text-center ${meta.height === status.height ? "bg-white text-black" : "bg-white/[0.03] text-muted hover:text-fg"}`}
              >
                <div className="font-mono text-xs tabular-nums">{meta.height.toLocaleString("en-US")}</div>
                <div className="mt-1 text-[10px]">{txCount(meta.numTxs)}</div>
              </Link>
            ))}
          </div>
        </Panel>

        <div className="grid min-h-0 flex-1 gap-4 lg:grid-cols-[1.4fr_1fr]">
          <Panel className="flex min-h-[280px] flex-col">
            <div className="border-b border-white/[0.06] px-5 py-3">
              <Kicker>Latest transactions</Kicker>
            </div>
            {latest.length === 0 ? <p className="px-5 py-4 text-sm text-muted">No transactions in the latest block.</p> : null}
            <ul className="flex-1 divide-y divide-white/[0.06]">
              {latest.map((hash, i) => (
                <li key={hash}>
                  <Link to={`/explorer/tx/${hash}`} className="grid grid-cols-[7rem_1fr_auto] items-center gap-3 px-5 py-3 text-sm hover:bg-white/[0.02]">
                    <span className="font-mono text-xs text-muted">{hash.slice(0, 8)}</span>
                    <span>
                      Transaction
                      <span className="ml-2 font-mono text-[10px] text-muted">#{status.height.toLocaleString("en-US")}</span>
                    </span>
                    <span className="font-mono text-xs text-muted">#{i}</span>
                  </Link>
                </li>
              ))}
            </ul>
            {head && head.txHashes.length > latest.length ? (
              <Link to={`/explorer/block/${head.height}`} className="border-t border-white/[0.06] px-5 py-3 text-sm text-muted hover:text-fg">
                {head.txHashes.length - latest.length} more in this block
              </Link>
            ) : null}
          </Panel>
          <div className="flex flex-col gap-4">
            <Panel className="px-5 py-4">
              <Kicker>Epoch</Kicker>
              <p className="mt-3 text-sm text-muted">This chain build has no emission query.</p>
            </Panel>
            <Panel className="flex-1">
              <div className="border-b border-white/[0.06] px-5 py-3">
                <Kicker>Voting power</Kicker>
              </div>
              {validators.validators.length === 0 ? <p className="px-5 py-4 text-sm text-muted">The validator set is empty.</p> : null}
              <ul className="max-h-80 overflow-y-auto">
                {validators.validators.map((v) => (
                  <li key={v.address} className="border-b border-white/[0.06] last:border-b-0">
                    <Link to={`/explorer/validator/${v.address}`} className="block px-5 py-3 hover:bg-white/[0.02]">
                      <div className="flex items-baseline justify-between text-sm">
                        <span className="font-mono text-xs">{short(v.address)}</span>
                        <span className="font-mono text-xs text-muted">{sharePct(v.power, validators.totalPower)}</span>
                      </div>
                      <div className="mt-2 h-1 overflow-hidden rounded-full bg-white/[0.06]">
                        <div className="h-full rounded-full bg-fg" style={{ width: shareWidth(v.power, validators.totalPower) }} />
                      </div>
                    </Link>
                  </li>
                ))}
              </ul>
            </Panel>
          </div>
        </div>
      </div>
    </ExplorerFrame>
  );
}

export function BlockPage() {
  const { height } = useParams();
  const key = heightKey(height);
  const state = useChain(key, loadBlock);
  return (
    <QueryBody state={state} invalid={key ? undefined : "That block height is not valid."}>
      {(block) => {
        const n = block.height;
        return (
          <ExplorerFrame>
            <Kicker>Block</Kicker>
            <h1 className="font-display text-3xl mt-1 tabular-nums">{n.toLocaleString("en-US")}</h1>
            <p className="font-mono text-xs uppercase tracking-widest text-muted mt-1">{block.chainId}</p>
            <div className="flex gap-4 text-sm mt-4 mb-8">
              {n > 1 ? (
                <Link className="text-muted hover:text-fg" to={`/explorer/block/${n - 1}`}>
                  ← {(n - 1).toLocaleString("en-US")}
                </Link>
              ) : null}
              <Link className="text-muted hover:text-fg" to={`/explorer/block/${n + 1}`}>
                {(n + 1).toLocaleString("en-US")} →
              </Link>
            </div>
            <dl className="mb-8 grid sm:grid-cols-2 gap-4 text-sm">
              <Row k="Time" v={when(block.time)} />
              <Row k="Transactions" v={txCount(block.txHashes.length)} />
              <Row
                k="Proposer"
                v={block.proposer ? short(block.proposer) : "Not in the header"}
                to={block.proposer ? `/explorer/validator/${block.proposer}` : undefined}
              />
              {block.appHash ? <Row k="App hash" v={short(block.appHash)} /> : null}
            </dl>
            {block.txHashes.length === 0 ? <p className="text-sm text-muted">No transactions in this block.</p> : null}
            <ul className="divide-y divide-border border border-border">
              {block.txHashes.map((hash) => (
                <li key={hash}>
                  <Link to={`/explorer/tx/${hash}`} className="grid grid-cols-1 sm:grid-cols-[8rem_1fr] gap-1 sm:gap-4 px-3 py-3 hover:bg-surface text-sm">
                    <span className="font-mono text-muted">{hash.slice(0, 8)}</span>
                    <span className="font-mono break-all">{hash}</span>
                  </Link>
                </li>
              ))}
            </ul>
          </ExplorerFrame>
        );
      }}
    </QueryBody>
  );
}

export function TxPage() {
  const { hash } = useParams();
  const key = txKey(hash);
  const state = useChain(key, getTx);
  return (
    <QueryBody state={state} invalid={key ? undefined : "That transaction hash is not valid."}>
      {(tx) => (
        <ExplorerFrame>
          <Kicker>{tx.code === 0 ? "Transaction" : "Failed transaction"}</Kicker>
          <h1 className="font-mono text-lg mt-2 break-all">{tx.hash}</h1>
          <p className="font-mono text-xs uppercase tracking-widest text-muted mt-1">
            {tx.code === 0 ? "Success" : `Code ${tx.code}`}
            {" · "}
            <Link className="hover:text-fg" to={`/explorer/block/${tx.height}`}>
              block {tx.height.toLocaleString("en-US")}
            </Link>
          </p>
          <dl className="mt-8 grid sm:grid-cols-2 gap-4 text-sm">
            <Row k="Index" v={String(tx.index)} />
            <Row k="Code" v={tx.code === 0 ? "0" : String(tx.code)} />
            <Row k="Gas wanted" v={tx.gasWanted == null ? "Not in the response" : tx.gasWanted.toLocaleString("en-US")} />
            <Row k="Gas used" v={tx.gasUsed == null ? "Not in the response" : tx.gasUsed.toLocaleString("en-US")} />
            {tx.codespace ? <Row k="Codespace" v={tx.codespace} /> : null}
          </dl>
          {tx.log ? (
            <pre className="mt-6 max-w-3xl whitespace-pre-wrap break-all font-mono text-xs text-muted">{clip(tx.log, 4000)}</pre>
          ) : null}
          <h2 className="font-display text-xl mt-10">Events</h2>
          {tx.events.length === 0 ? <p className="mt-3 text-sm text-muted">This transaction published no events.</p> : null}
          <ul className="mt-3 divide-y divide-border border border-border text-sm">
            {tx.events.slice(0, 40).map((ev, i) => (
              <li key={`${ev.type}-${i}`} className="px-3 py-3">
                <p className="font-mono text-xs text-muted">{ev.type || "event"}</p>
                <ul className="mt-2 space-y-1">
                  {ev.attributes.map((attr, j) => (
                    <li key={j} className="break-all">
                      <span className="text-muted">{clip(attr.key, 80)}</span>
                      <span className="mx-2 text-muted">=</span>
                      {clip(attr.value, 300)}
                    </li>
                  ))}
                </ul>
              </li>
            ))}
          </ul>
          {tx.events.length > 40 ? <p className="mt-3 text-sm text-muted">{tx.events.length - 40} more events are not shown.</p> : null}
        </ExplorerFrame>
      )}
    </QueryBody>
  );
}

export function EpochPage() {
  return (
    <ExplorerFrame>
      <Kicker>Epoch</Kicker>
      <h1 className="font-display text-3xl mt-1">Epoch</h1>
      <p className="mt-4 max-w-xl text-sm text-muted">This chain build has no emission query.</p>
    </ExplorerFrame>
  );
}

export function AccountPage() {
  const { address } = useParams();
  const key = accountKey(address);
  const state = useChain(key, loadAccount);
  return (
    <QueryBody state={state} invalid={key ? undefined : "That account address is not a valid orama address."}>
      {(data) => (
        <ExplorerFrame>
          <Kicker>Wallet</Kicker>
          <h1 className="font-mono text-lg mt-2 break-all">{data.address}</h1>
          <p className="mt-2 font-mono text-xs uppercase tracking-widest text-muted">{data.status.chainId}</p>
          <p className="mt-6 max-w-xl text-sm text-muted">
            This proxy does not serve a per-account balance, earnings, or delegation query.
          </p>
        </ExplorerFrame>
      )}
    </QueryBody>
  );
}

export function ValidatorPage() {
  const { address } = useParams();
  const key = validatorKey(address);
  const state = useChain(key, loadValidator);
  return (
    <QueryBody state={state} invalid={key ? undefined : "That validator address is not valid."}>
      {(data) => (
        <ExplorerFrame>
          <Kicker>Validator</Kicker>
          <h1 className="font-mono text-lg mt-2 break-all">{data.match?.address ?? data.address}</h1>
          {data.match ? (
            <>
              <div className="grid sm:grid-cols-3 gap-3 mt-6">
                <Stat label="Voting power" value={formatBig(data.match.power)} />
                <Stat label="Share" value={sharePct(data.match.power, data.set.totalPower)} hint="Of this CometBFT set" />
                <Stat label="Proposer priority" value={formatBig(data.match.priority)} />
              </div>
              <p className="mt-6 max-w-xl text-sm text-muted">
                Stake, commission, and the ramp are not in the CometBFT validator query.
              </p>
            </>
          ) : (
            <p className="mt-6 max-w-xl text-sm text-muted">
              {bech32Hrp(data.address)
                ? "CometBFT's validator query returns consensus addresses, not operator addresses."
                : "That address is not in the current CometBFT validator set."}
            </p>
          )}
        </ExplorerFrame>
      )}
    </QueryBody>
  );
}

export function CommitteePage() {
  return (
    <ExplorerFrame>
      <Kicker>Bootstrap committee</Kicker>
      <h1 className="font-display text-3xl mt-1">Hand-over</h1>
      <p className="mt-4 max-w-xl text-sm text-muted">This chain build has no power query.</p>
    </ExplorerFrame>
  );
}

export function SupplyPage() {
  const state = useChain("supply", loadSupply);
  return (
    <QueryBody state={state}>
      {(data) => (
        <ExplorerFrame>
          <Kicker>Supply</Kicker>
          <h1 className="font-display text-3xl mt-1">What exists</h1>
          <div className="grid sm:grid-cols-3 gap-3 mt-6">
            <Stat label="Bank supply" value={`${formatNorama(data.supply.amount)} ORAMA`} hint="norama, every account" />
            <Stat label="Bonded" value={`${formatNorama(data.pool.bonded)} ORAMA`} hint="Staking pool" />
            <Stat label="Not bonded" value={`${formatNorama(data.pool.notBonded)} ORAMA`} hint="Staking pool" />
          </div>
          <p className="text-sm text-muted mt-6 max-w-xl">
            This chain build has no emission query, so minted and burned are not split here.
          </p>
        </ExplorerFrame>
      )}
    </QueryBody>
  );
}

export function PoolsPage() {
  return (
    <ExplorerFrame>
      <Kicker>Pools</Kicker>
      <h1 className="font-display text-3xl mt-1">Pools</h1>
      <p className="mt-4 max-w-xl text-sm text-muted">This chain build has no shielded pool query.</p>
    </ExplorerFrame>
  );
}

export function HousesPage() {
  return (
    <ExplorerFrame>
      <Kicker>Houses</Kicker>
      <h1 className="font-display text-3xl mt-1">Two houses</h1>
      <p className="mt-4 max-w-xl text-sm text-muted">This chain build has no houses query.</p>
    </ExplorerFrame>
  );
}

export function SearchPage() {
  const [params] = useSearchParams();
  const q = (params.get("q") ?? "").trim();
  const state = useChain(q, loadSearch);
  return (
    <ExplorerFrame>
      <Kicker>Search</Kicker>
      <h1 className="font-display text-3xl mt-1 break-all">{q || "Empty"}</h1>
      {!q ? (
        <p className="mt-4 max-w-xl text-sm text-muted">
          Search a block height, a transaction hash, an orama address, or a validator address.
        </p>
      ) : null}
      {q && !state.data && state.phase === "loading" ? <p className="mt-4 text-sm text-muted">Loading the chain.</p> : null}
      {q && !state.data && state.phase === "error" ? (
        <p className="mt-4 max-w-xl break-all text-sm text-muted">{state.error}</p>
      ) : null}
      {state.data ? <SearchBody hit={state.data} /> : null}
      {state.data && state.phase === "error" ? <Banner message={state.error} /> : null}
    </ExplorerFrame>
  );
}

function SearchBody({ hit }: { hit: SearchData }) {
  switch (hit.kind) {
    case "none":
      return <p className="mt-4 text-sm text-muted">Nothing matched.</p>;
    case "shielded":
      return <p className="mt-4 max-w-xl text-sm text-muted">This chain build has no shielded query.</p>;
    case "block":
      return <Go to={`/explorer/block/${hit.height}`} label={`Block ${hit.height.toLocaleString("en-US")}`} />;
    case "tx":
      return <Go to={`/explorer/tx/${hit.hash}`} label={`Transaction in block ${hit.height.toLocaleString("en-US")}`} />;
    case "account":
      return (
        <>
          <p className="mt-4 max-w-xl text-sm text-muted">
            This proxy does not serve a per-account balance, earnings, or delegation query.
          </p>
          <p className="mt-2 font-mono text-xs text-muted">{hit.chainId}</p>
          <Go to={`/explorer/account/${hit.address}`} label={short(hit.address)} />
        </>
      );
    case "validator":
      return hit.found ? (
        <Go to={`/explorer/validator/${hit.address}`} label={`Validator ${short(hit.address)}`} />
      ) : (
        <p className="mt-4 max-w-xl text-sm text-muted">{hit.note}</p>
      );
  }
}

function Go({ to, label }: { to: string; label: string }) {
  return (
    <Link to={to} className="inline-block mt-6 border border-fg px-3 py-2 text-sm">
      {label}
    </Link>
  );
}

function Row({ k, v, to }: { k: string; v: string; to?: string }) {
  return (
    <div className="border border-border px-3 py-2">
      <Kicker>{k}</Kicker>
      {to ? (
        <Link to={to} className="mt-1 block break-all hover:underline">
          {v}
        </Link>
      ) : (
        <p className="mt-1 break-all">{v}</p>
      )}
    </div>
  );
}
