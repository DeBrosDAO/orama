import { ArrowUpRight, Check, KeyRound, Lock, Server, ShieldCheck } from "lucide-react";
import { ANCHAT_SCREENS, APPS } from "../../content/apps";
import type { AppShowcase } from "../../content/apps";
import { cn } from "../../lib/utils";

function AppHeader({ app }: { app: AppShowcase }) {
  return (
    <div className="flex items-center gap-4">
      <span className="flex items-center justify-center w-14 h-14 rounded-2xl border border-border bg-surface-2">
        <img src={app.mark} alt="" className="w-8 h-8 object-contain" loading="lazy" />
      </span>
      <div className="flex flex-col gap-1">
        <h2 className="font-display font-bold text-2xl text-fg">{app.name}</h2>
        <span className="font-mono text-[11px] tracking-wider uppercase text-muted">{app.status}</span>
      </div>
    </div>
  );
}

function AppBody({ app }: { app: AppShowcase }) {
  return (
    <div className="flex flex-col gap-6">
      <AppHeader app={app} />
      <p className="font-display text-xl sm:text-2xl text-fg leading-snug text-balance">{app.tagline}</p>
      <ul className="flex flex-col gap-2.5">
        {app.facts.map((f) => (
          <li key={f} className="flex items-start gap-3 text-sm text-accent">
            <Check size={16} className="mt-0.5 shrink-0 text-fg" />
            {f}
          </li>
        ))}
      </ul>
      <div className="flex items-baseline gap-3 border-l border-fg/30 pl-4">
        <span className="font-display font-bold text-4xl text-fg tabular-nums">{app.metric.value}</span>
        <span className="text-sm text-muted">{app.metric.label}</span>
      </div>
      <div className="flex flex-col gap-2">
        <span className="font-mono text-[11px] tracking-[0.2em] uppercase text-muted">{app.relation}</span>
        <div className="flex flex-wrap gap-2">
          {app.poweredBy.map((p) => (
            <span key={p} className="px-2.5 py-1 border border-dashed border-border text-xs text-accent-2">
              {p}
            </span>
          ))}
        </div>
      </div>
      <div className="flex flex-wrap gap-2">
        {app.links.map((l) => (
          <a
            key={l.href}
            href={l.href}
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex items-center gap-1.5 px-3.5 py-2 border border-border/70 font-mono text-xs uppercase tracking-wider text-muted hover:text-fg hover:border-fg/30 transition-colors"
          >
            {l.label}
            <ArrowUpRight size={12} />
          </a>
        ))}
      </div>
    </div>
  );
}

function AnChatScreens() {
  return (
    <div className="relative flex justify-center gap-3 sm:gap-4">
      {ANCHAT_SCREENS.map((s, i) => (
        <img
          key={s.src}
          src={s.src}
          alt={s.alt}
          loading="lazy"
          width={s.width}
          height={s.height}
          className={cn(
            "w-[30%] max-w-[190px] h-auto rounded-[1.4rem] border border-border/60 shadow-[0_20px_60px_rgba(0,0,0,0.6)]",
            i === 1 ? "-translate-y-4 sm:-translate-y-8" : "opacity-80",
          )}
        />
      ))}
    </div>
  );
}

/** RootWallet has no marketing screenshots; this draws what it does instead. */
function RootWalletVisual({ mark }: { mark: string }) {
  const items = [
    { icon: KeyRound, label: "Crypto keys" },
    { icon: Lock, label: "Passwords" },
    { icon: Server, label: "SSH keys" },
    { icon: ShieldCheck, label: "2FA codes" },
  ];
  return (
    <div className="flex flex-col items-center gap-6">
      <div className="flex items-center gap-3 px-5 py-3 border border-fg/30 bg-surface-2 rounded-sm">
        <img src={mark} alt="" className="w-6 h-6" loading="lazy" />
        <span className="font-mono text-xs tracking-[0.2em] uppercase text-fg">One wallet</span>
      </div>
      <svg viewBox="0 0 320 40" className="w-full max-w-[340px] h-10" aria-hidden="true" fill="none">
        {[40, 120, 200, 280].map((x) => (
          <path key={x} d={`M160 0 C160 20, ${x} 20, ${x} 40`} className="stroke-accent/50 animate-flow-slow" strokeWidth={1.25} />
        ))}
      </svg>
      <div className="grid grid-cols-4 gap-2 sm:gap-3 w-full max-w-[380px]">
        {items.map(({ icon: Icon, label }) => (
          <div key={label} className="flex flex-col items-center gap-2 p-3 border border-dashed border-border">
            <Icon size={18} className="text-fg" />
            <span className="text-[10px] sm:text-[11px] font-mono uppercase tracking-wider text-muted text-center">{label}</span>
          </div>
        ))}
      </div>
      <div className="flex items-center gap-2 text-xs text-muted">
        <span className="w-1.5 h-1.5 rounded-full bg-fg animate-pulse-dot text-fg" />
        Signs you in to Orama. No account, no password.
      </div>
    </div>
  );
}

export function AppShowcaseBlock({ id }: { id: AppShowcase["id"] }) {
  const app = APPS.find((a) => a.id === id);
  if (!app) throw new Error(`AppShowcaseBlock: unknown app "${id}"`);
  const flip = id === "rootwallet";
  return (
    <div className="grid grid-cols-1 lg:grid-cols-2 gap-12 lg:gap-16 items-center">
      <div className={cn(flip && "lg:order-2")}>
        <AppBody app={app} />
      </div>
      <div className={cn("py-6", flip && "lg:order-1")}>
        {id === "anchat" ? <AnChatScreens /> : <RootWalletVisual mark={app.mark} />}
      </div>
    </div>
  );
}
