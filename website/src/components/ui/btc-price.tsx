import { createContext, useContext, useEffect, useState, type ReactNode } from "react";
import { formatBTC } from "../../data/fundraise";

/* ── BTC/USD Price Context (single source of truth) ── */

interface BTCPriceContextValue {
  btcUsd: number | null;
  loading: boolean;
}

const BTCPriceContext = createContext<BTCPriceContextValue>({ btcUsd: null, loading: true });

const CACHE_KEY = "orama_btc_usd";
const CACHE_TTL = 10 * 60 * 1000; // 10 minutes

function getCached(): number | null {
  try {
    const raw = localStorage.getItem(CACHE_KEY);
    if (!raw) return null;
    const { price, ts } = JSON.parse(raw);
    if (Date.now() - ts < CACHE_TTL) return price;
  } catch { /* ignore */ }
  return null;
}

function setCache(price: number) {
  localStorage.setItem(CACHE_KEY, JSON.stringify({ price, ts: Date.now() }));
}

export function BTCPriceProvider({ children }: { children: ReactNode }) {
  const [btcUsd, setBtcUsd] = useState<number | null>(getCached);
  const [loading, setLoading] = useState(btcUsd === null);

  useEffect(() => {
    const fetchPrice = async () => {
      const cached = getCached();
      if (cached) {
        setBtcUsd(cached);
        setLoading(false);
        return;
      }
      try {
        const res = await fetch("https://api.coingecko.com/api/v3/simple/price?ids=bitcoin&vs_currencies=usd");
        const data = await res.json();
        const price = data.bitcoin?.usd;
        if (price) {
          setBtcUsd(price);
          setCache(price);
        }
      } catch { /* silent fail */ }
      setLoading(false);
    };

    fetchPrice();
    const interval = setInterval(fetchPrice, CACHE_TTL);
    return () => clearInterval(interval);
  }, []);

  return (
    <BTCPriceContext.Provider value={{ btcUsd, loading }}>
      {children}
    </BTCPriceContext.Provider>
  );
}

export function useBTCPrice() {
  return useContext(BTCPriceContext);
}

/* ── Helper: format USD ── */
function fmtUsd(usd: number): string {
  if (usd >= 1_000_000) return `$${(usd / 1_000_000).toFixed(1)}M`;
  if (usd >= 1_000) return `$${usd.toLocaleString(undefined, { maximumFractionDigits: 0 })}`;
  if (usd >= 1) return `$${usd.toFixed(2)}`;
  return `$${usd.toFixed(4)}`;
}

/* ── BTC orange color ── */
const BTC_COLOR = "#F7931A";

/* ── Reusable BTC Price Display Component ── */

interface BTCPriceProps {
  btc: number;
  size?: "sm" | "md" | "lg" | "xl";
  hideUsd?: boolean;
  className?: string;
}

const sizeClasses = {
  sm: { btc: "text-xs font-mono font-bold", usd: "text-[10px]" },
  md: { btc: "text-sm font-mono font-bold", usd: "text-[10px]" },
  lg: { btc: "text-lg font-mono font-bold", usd: "text-xs" },
  xl: { btc: "text-3xl font-display font-bold", usd: "text-xs" },
};

export function BTCPrice({ btc, size = "md", hideUsd = false, className = "" }: BTCPriceProps) {
  const { btcUsd } = useBTCPrice();
  const classes = sizeClasses[size];
  const usdValue = btcUsd ? btc * btcUsd : null;

  return (
    <span className={`inline-flex flex-col ${className}`}>
      <span className={classes.btc}>
        <span className="text-fg">{formatBTC(btc)}</span>{" "}
        <span style={{ color: BTC_COLOR }}>BTC</span>
      </span>
      {!hideUsd && usdValue !== null && (
        <span className={`${classes.usd} font-mono text-muted`}>
          ≈ {fmtUsd(usdValue)}
        </span>
      )}
    </span>
  );
}

/**
 * Inline version — BTC with USD on the same line.
 */
export function BTCPriceInline({ btc, className = "" }: { btc: number; className?: string }) {
  const { btcUsd } = useBTCPrice();
  const usdValue = btcUsd ? btc * btcUsd : null;

  return (
    <span className={className}>
      <span className="font-mono font-bold">
        <span className="text-fg">{formatBTC(btc)}</span>{" "}
        <span style={{ color: BTC_COLOR }}>BTC</span>
      </span>
      {usdValue !== null && (
        <span className="text-muted ml-1.5 text-[10px] font-mono">
          ≈ {fmtUsd(usdValue)}
        </span>
      )}
    </span>
  );
}

/**
 * Just the USD equivalent text for a BTC amount (for fundraise bars etc).
 */
export function BTCtoUSD({ btc, className = "" }: { btc: number; className?: string }) {
  const { btcUsd } = useBTCPrice();
  if (!btcUsd) return null;
  const usdValue = btc * btcUsd;
  return (
    <span className={`text-[10px] font-mono text-muted ${className}`}>
      ≈ {fmtUsd(usdValue)}
    </span>
  );
}
