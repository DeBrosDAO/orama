import { useEffect, useRef, useState, useCallback, lazy, Suspense } from "react";
import { Link } from "react-router";
import {
  ExternalLink,
  ShieldCheck,
  Code,
  Users,
  Eye,
  Coins,
  Rocket,
  Github,
  Server,
  AppWindow,
  Handshake,
  ArrowRight,
  Play,
  Pause,
} from "lucide-react";
import { Page } from "../components/layout/page";
import { Section } from "../components/layout/section";
import { SectionHeader } from "../components/ui/section-header";
import { DashedPanel } from "../components/ui/dashed-panel";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { CrosshairDivider } from "../components/ui/crosshair-divider";
import { AnimateIn } from "../components/ui/animate-in";
import { SplitText } from "../components/ui/split-text";
import { SILVER } from "../components/ui/silver-theme";
import { Redacted } from "../components/ui/redacted";

const AboutHeroScene = lazy(() =>
  import("../components/landing/about-hero-scene").then((m) => ({
    default: m.AboutHeroScene,
  })),
);

/* ═══════════════════════════════════════════
   X Spaces embed component
   ═══════════════════════════════════════════ */

const X_SPACES = [
  { id: "1vAGRDWDBADxl", url: "https://x.com/i/spaces/1vAGRDWDBADxl?s=20", title: "Introducing AnChat: Your Web3 Decentralized Privacy Chat Platform" },
  { id: "1OyKAjOAZEqGb", url: "https://x.com/i/spaces/1OyKAjOAZEqGb?s=20", title: "DeBros Unchained: Privacy, Chat & Network Revolution with ANyONe" },
  { id: "1RKjpzNmgwjJw", url: "https://x.com/i/spaces/1RKjpzNmgwjJw?s=20", title: "AnChat is LIVE: Ask Us Anything" },
];

/* ═══════════════════════════════════════════
   Floating Voice Player (bottom bar)
   ═══════════════════════════════════════════ */

const VOICE_SRC = "/aboutus.wav";
const BAR_COUNT = 64;

function FloatingVoicePlayer() {
  const audioRef = useRef<HTMLAudioElement | null>(null);
  const analyserRef = useRef<AnalyserNode | null>(null);
  const contextRef = useRef<AudioContext | null>(null);
  const rafRef = useRef<number>(0);
  const [isPlaying, setIsPlaying] = useState(false);
  const [bars, setBars] = useState<number[]>(new Array(BAR_COUNT).fill(0));
  const [progress, setProgress] = useState(0);
  const [dismissed, setDismissed] = useState(false);

  const setupAnalyser = useCallback(() => {
    if (contextRef.current || !audioRef.current) return;
    const ctx = new AudioContext();
    const analyser = ctx.createAnalyser();
    analyser.fftSize = 256;
    const source = ctx.createMediaElementSource(audioRef.current);
    source.connect(analyser);
    analyser.connect(ctx.destination);
    contextRef.current = ctx;
    analyserRef.current = analyser;
  }, []);

  const animate = useCallback(() => {
    if (!analyserRef.current) return;
    const data = new Uint8Array(analyserRef.current.frequencyBinCount);
    analyserRef.current.getByteFrequencyData(data);

    // Use only the lower ~70% of frequency bins (where voice energy lives)
    // and spread them across all bars with averaging for smooth output
    const usableBins = Math.floor(data.length * 0.7);
    const newBars: number[] = [];
    for (let i = 0; i < BAR_COUNT; i++) {
      const startBin = Math.floor((i / BAR_COUNT) * usableBins);
      const endBin = Math.floor(((i + 1) / BAR_COUNT) * usableBins);
      let sum = 0;
      const count = Math.max(1, endBin - startBin);
      for (let b = startBin; b < endBin; b++) {
        sum += data[b];
      }
      newBars.push((sum / count) / 255);
    }
    setBars(newBars);

    if (audioRef.current) {
      setProgress(audioRef.current.currentTime / (audioRef.current.duration || 1));
    }
    rafRef.current = requestAnimationFrame(animate);
  }, []);

  const togglePlay = useCallback(() => {
    if (!audioRef.current) return;

    // Lazily create AudioContext on first user gesture
    if (!contextRef.current) setupAnalyser();

    if (isPlaying) {
      audioRef.current.pause();
      cancelAnimationFrame(rafRef.current);
      setBars(new Array(BAR_COUNT).fill(0));
      setIsPlaying(false);
    } else {
      if (contextRef.current?.state === "suspended") contextRef.current.resume();
      audioRef.current.play();
      setIsPlaying(true);
      // Switch to real analyser-driven animation
      cancelAnimationFrame(rafRef.current);
      rafRef.current = requestAnimationFrame(animate);
    }
  }, [isPlaying, setupAnalyser, animate]);

  useEffect(() => {
    const audio = new Audio(VOICE_SRC);
    audio.crossOrigin = "anonymous";
    audio.preload = "auto";
    audioRef.current = audio;

    const onEnded = () => {
      setIsPlaying(false);
      cancelAnimationFrame(rafRef.current);
      setBars(new Array(BAR_COUNT).fill(0));
      setProgress(0);
    };
    audio.addEventListener("ended", onEnded);

    // Auto-play without AudioContext (browsers allow audio.play() more often than AudioContext)
    const tryAutoPlay = async () => {
      try {
        await audio.play();
        setIsPlaying(true);
        // Start a simple progress loop (no waveform until user interacts)
        const tick = () => {
          if (audioRef.current) {
            setProgress(audioRef.current.currentTime / (audioRef.current.duration || 1));
            // Fake waveform from time-based noise when analyser isn't ready
            if (!analyserRef.current) {
              const t = audioRef.current.currentTime;
              const fakeBars: number[] = [];
              for (let i = 0; i < BAR_COUNT; i++) {
                fakeBars.push(0.3 + 0.4 * Math.abs(Math.sin(t * 3 + i * 0.4) * Math.cos(t * 1.7 + i * 0.2)));
              }
              setBars(fakeBars);
            }
          }
          rafRef.current = requestAnimationFrame(tick);
        };
        rafRef.current = requestAnimationFrame(tick);
      } catch {
        // Autoplay fully blocked — user will click play
      }
    };
    tryAutoPlay();

    return () => {
      audio.removeEventListener("ended", onEnded);
      audio.pause();
      cancelAnimationFrame(rafRef.current);
      if (contextRef.current) contextRef.current.close();
    };
  }, []);

  const seekBarRef = useRef<HTMLDivElement>(null);
  const [isHovering, setIsHovering] = useState(false);
  const [hoverX, setHoverX] = useState(0);

  const handleSeek = useCallback((e: React.MouseEvent<HTMLDivElement>) => {
    if (!audioRef.current || !seekBarRef.current) return;
    const rect = seekBarRef.current.getBoundingClientRect();
    const pct = Math.max(0, Math.min(1, (e.clientX - rect.left) / rect.width));
    audioRef.current.currentTime = pct * (audioRef.current.duration || 0);
    setProgress(pct);
  }, []);

  const handleHover = useCallback((e: React.MouseEvent<HTMLDivElement>) => {
    if (!seekBarRef.current) return;
    const rect = seekBarRef.current.getBoundingClientRect();
    setHoverX(Math.max(0, Math.min(1, (e.clientX - rect.left) / rect.width)));
  }, []);

  const formatTime = (sec: number) => {
    const m = Math.floor(sec / 60);
    const s = Math.floor(sec % 60);
    return `${m}:${s.toString().padStart(2, "0")}`;
  };

  if (dismissed) return null;

  const currentTime = audioRef.current?.currentTime ?? 0;
  const duration = audioRef.current?.duration ?? 0;

  return (
    <div className="fixed bottom-0 left-0 right-0 z-50">
      {/* Seekable progress bar */}
      <div
        ref={seekBarRef}
        className="h-[3px] bg-border/20 cursor-pointer group/seek relative"
        onClick={handleSeek}
        onMouseEnter={() => setIsHovering(true)}
        onMouseLeave={() => setIsHovering(false)}
        onMouseMove={handleHover}
        style={{ transition: "height 150ms" }}
        onMouseOver={(e) => { (e.currentTarget as HTMLDivElement).style.height = "5px"; }}
        onMouseOut={(e) => { (e.currentTarget as HTMLDivElement).style.height = "3px"; }}
      >
        {/* Played portion */}
        <div
          className="absolute top-0 left-0 h-full"
          style={{ width: `${progress * 100}%`, background: SILVER.gradient }}
        />
        {/* Hover preview */}
        {isHovering && (
          <div
            className="absolute top-0 left-0 h-full bg-white/10"
            style={{ width: `${hoverX * 100}%` }}
          />
        )}
        {/* Scrub dot */}
        <div
          className="absolute top-1/2 -translate-y-1/2 w-3 h-3 rounded-full bg-fg shadow-lg transition-opacity duration-150"
          style={{
            left: `${progress * 100}%`,
            transform: `translate(-50%, -50%)`,
            opacity: isHovering ? 1 : 0,
          }}
        />
      </div>

      <div
        className="backdrop-blur-xl border-t border-dashed border-border"
        style={{ background: "rgba(0,0,0,0.85)" }}
      >
        <div className="px-3 sm:px-4 py-2.5 flex items-center gap-2 sm:gap-3">
          {/* Play / Pause */}
          <button
            type="button"
            onClick={togglePlay}
            className="w-8 h-8 rounded-full flex items-center justify-center shrink-0 border border-dashed border-border hover:border-fg/30 transition-all cursor-pointer group"
            style={{ background: isPlaying ? SILVER.bg : "transparent" }}
          >
            {isPlaying ? (
              <Pause className="w-3 h-3 text-fg" />
            ) : (
              <Play className="w-3 h-3 text-muted group-hover:text-fg transition-colors ml-0.5" />
            )}
          </button>

          {/* Time */}
          <span className="text-[10px] font-mono text-muted tabular-nums shrink-0 w-14 sm:w-[70px] hidden sm:inline">
            {formatTime(currentTime)} / {formatTime(duration)}
          </span>

          {/* Full-width waveform bars */}
          <div className="flex-1 flex items-center gap-[1px] h-7">
            {bars.map((val, i) => {
              const idleH = 2 + Math.sin(i * 0.15) * 2;
              const h = isPlaying ? Math.max(val * 28, 1.5) : idleH;
              const played = i / BAR_COUNT <= progress;
              return (
                <div
                  key={i}
                  className="flex-1 rounded-full"
                  style={{
                    height: `${h}px`,
                    minWidth: "1px",
                    background: played
                      ? SILVER.light
                      : `rgba(161,161,170,${isPlaying ? 0.3 : 0.1})`,
                    transition: isPlaying ? "height 50ms" : "height 600ms",
                  }}
                />
              );
            })}
          </div>

          {/* Label */}
          <span className="text-[10px] font-mono text-muted tracking-wider uppercase shrink-0 hidden sm:block">
            {isPlaying ? "Playing" : "Our Story"}
          </span>

          {/* Close */}
          <button
            type="button"
            onClick={() => {
              if (audioRef.current) audioRef.current.pause();
              cancelAnimationFrame(rafRef.current);
              setDismissed(true);
            }}
            className="text-muted hover:text-fg transition-colors cursor-pointer shrink-0 text-xs font-mono"
          >
            ✕
          </button>
        </div>
      </div>
    </div>
  );
}

/* ═══════════════════════════════════════════
   Data
   ═══════════════════════════════════════════ */

const VALUES = [
  {
    icon: <ShieldCheck className="w-5 h-5" />,
    title: "Privacy First",
    description: "We don't collect data, period. No analytics, no tracking, no telemetry. Your infrastructure, your business.",
  },
  {
    icon: <Code className="w-5 h-5" />,
    title: "Open Source Always",
    description: "Every line of code is public. From the Go backend to this website. Audit it, fork it, verify every claim we make.",
  },
  {
    icon: <Users className="w-5 h-5" />,
    title: "Community Owned",
    description: "No VC money. No corporate board. Governance belongs to the people who run the nodes and build on the network.",
  },
  {
    icon: <Eye className="w-5 h-5" />,
    title: "Transparency",
    description: "Wallets are public. Code is auditable. Tokenomics are on-chain. If we say something, you can verify it yourself.",
  },
  {
    icon: <Coins className="w-5 h-5" />,
    title: "Fair Economics",
    description: "Node operators earn, not middlemen. Developers pay only for what they use. No hidden fees, no markup, no rent-seeking.",
  },
  {
    icon: <Rocket className="w-5 h-5" />,
    title: "Long-term Vision",
    description: "Building for 2028 mainnet, not quick flips. Every decision optimizes for the network existing in 10 years, not 10 months.",
  },
];

const TEAM_MEMBERS = [
  { name: "@johnysigma", role: "Co-Founder", image: "/images/js.jpg", url: "https://x.com/johnysigma" },
  { name: "@KMDevOps", role: "Co-Founder", image: "/images/km.jpg", url: "https://x.com/KMDevOps" },
  { name: "@_anonpenguin_", role: "Core Team", image: "/images/pen.jpg", url: "https://x.com/_anonpenguin_" },
  { name: "@_anonnik_", role: "Core Team", image: "/images/nik.jpg", url: "https://x.com/_anonnik_" },
];

const TRUST_METRICS = [
  { icon: <Server className="w-6 h-6" />, label: "Nodes Live", value: "50+" },
  { icon: <AppWindow className="w-6 h-6" />, label: "Live Apps", value: "AnChat" },
  { icon: <Github className="w-6 h-6" />, label: "GitHub Repos", value: "Public" },
  { icon: <Handshake className="w-6 h-6" />, label: "Privacy Layer", value: "Orama Proxy" },
];

/* ═══════════════════════════════════════════
   Page
   ═══════════════════════════════════════════ */

export default function About() {
  return (
    <Page title="About">
      {/* ── Hero ── */}
      <Section padding="wide">
        <div className="about-hero flex flex-col items-center text-center min-h-[70vh] pt-[12vh] gap-6 max-w-3xl mx-auto">
          <Badge variant="default" className="w-fit">
            ABOUT DEBROS
          </Badge>

          <h1 className="font-display font-bold text-4xl lg:text-6xl leading-tight">
            <SplitText
              text="We're DeBros."
              className="text-fg"
              delay={30}
              duration={0.6}
              splitType="chars"
              from={{ opacity: 0, y: 30 }}
              to={{ opacity: 1, y: 0 }}
            />
            <br />
            <SplitText
              text="We build what we believe in."
              delay={30}
              duration={0.6}
              splitType="chars"
              from={{ opacity: 0, y: 30 }}
              to={{ opacity: 1, y: 0 }}
              className=""
            />
          </h1>

          <style>{`
            .about-hero h1 > span:last-of-type .split-char {
              background: ${SILVER.gradient};
              -webkit-background-clip: text;
              -webkit-text-fill-color: transparent;
            }
          `}</style>

          <p className="text-muted text-sm leading-relaxed max-w-lg">
            We're a small team of builders who got tired of handing our
            infrastructure to corporations. So we built our own — open source,
            community owned, running on a decentralized mesh of independent
            operators. No pitch decks. No empty promises. Just working code.
          </p>

          <div className="flex flex-col sm:flex-row gap-3 mt-2">
            <Button asChild size="lg">
              <a
                href="https://github.com/debros"
                target="_blank"
                rel="noopener noreferrer"
              >
                View Our GitHub
                <ExternalLink className="w-3.5 h-3.5 ml-2" />
              </a>
            </Button>
            <Button asChild variant="ghost" size="lg">
              <Link to="/investors">
                Become an Investor
                <ArrowRight className="w-3.5 h-3.5 ml-2" />
              </Link>
            </Button>
          </div>
        </div>
      </Section>

      {/* ── Three.js Hero Scene ── */}
      <Suspense fallback={null}>
        <AboutHeroScene />
      </Suspense>

      <Section padding="none">
        <CrosshairDivider />
      </Section>

      {/* ── The Story ── */}
      <Section>
        <div className="flex flex-col gap-8">
          <SectionHeader title="The Story" />
          <AnimateIn>
            <DashedPanel withCorners withBackground className="p-6 sm:p-10">
              <div className="flex flex-col gap-6 max-w-3xl mx-auto text-muted leading-relaxed text-sm sm:text-base">
                <p>
                  We didn't start with funding. We didn't start with a team.
                  We started with two people — and{" "}
                  <span className="text-fg font-medium">
                    a conviction that wouldn't die.
                  </span>
                </p>
                <p>
                  Two builders. Working two jobs each. Pouring every paycheck,
                  every late night, every last bit of energy into something the
                  world told them was impossible. They believed that privacy is
                  not a feature —{" "}
                  <span className="text-fg font-medium">it's a right.</span>
                  {" "}They believed that your data, your messages, your identity
                  should belong to you — not to a corporation mining it for profit.
                </p>
                <p>
                  So they built AnChat. A messaging app where no one can read
                  your messages. Not governments. Not corporations.{" "}
                  <span className="text-fg font-medium">Not even us.</span>
                </p>
                <p>
                  Then two more joined. Four people now. Still no investors. Still
                  no safety net. They built the first physical hub. Worked day and
                  night. Faced attacks, legal threats, health problems. Invested
                  over{" "}
                  <span className="text-fg font-medium">
                    <Redacted /> of their own money.
                  </span>
                  {" "}Not because it was easy — because it mattered.
                </p>
                <p>
                  From that sacrifice came the Orama Network. A decentralized
                  cloud where your apps run on a mesh of independent nodes,
                  connected by encrypted tunnels. No Amazon. No Google. No single
                  point of failure. Fifty nodes are live today. Open source.
                  Auditable.{" "}
                  <span className="text-fg font-medium">
                    Owned by the community.
                  </span>
                </p>
                <p className="text-fg font-medium">
                  We are DeBros. We don't make promises we can't ship. We don't
                  take shortcuts we can't defend. And we will not stop — until the
                  infrastructure of the internet belongs to the people who use it.
                </p>
              </div>
            </DashedPanel>
          </AnimateIn>
        </div>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>

      {/* ── X Spaces ── */}
      <Section>
        <div className="flex flex-col gap-8">
          <SectionHeader title="X Spaces" />
          <AnimateIn>
            <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
              {X_SPACES.map((space) => (
                <a
                  key={space.id}
                  href={space.url}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="block group"
                >
                  <DashedPanel withCorners withBackground className="h-full hover:bg-white/[0.02] transition-colors">
                    <div className="flex flex-col items-center justify-center gap-4 p-8">
                      <div
                        className="w-14 h-14 rounded-full flex items-center justify-center"
                        style={{ border: `1px dashed ${SILVER.border}`, background: SILVER.bg }}
                      >
                        <svg viewBox="0 0 24 24" className="w-6 h-6 text-fg" fill="currentColor">
                          <path d="M8 5v14l11-7z" />
                        </svg>
                      </div>
                      <div className="flex flex-col items-center gap-1">
                        <span className="font-display font-bold text-fg text-sm group-hover:text-white transition-colors text-center">
                          {space.title}
                        </span>
                        <span className="text-[10px] font-mono text-muted tracking-wider uppercase">
                          Listen on X
                        </span>
                      </div>
                      <ExternalLink className="w-3 h-3 text-muted/40 group-hover:text-muted transition-colors" />
                    </div>
                  </DashedPanel>
                </a>
              ))}
            </div>
          </AnimateIn>
        </div>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>

      {/* ── Values & Ethics ── */}
      <Section>
        <div className="flex flex-col gap-8">
          <SectionHeader title="Values & Ethics" />
          <AnimateIn>
            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
              {VALUES.map((v) => (
                <DashedPanel key={v.title} withCorners className="p-5 sm:p-6">
                  <div className="flex flex-col gap-3">
                    <div className="text-accent">{v.icon}</div>
                    <h3 className="font-display font-semibold text-fg">
                      {v.title}
                    </h3>
                    <p className="text-sm text-muted leading-relaxed">
                      {v.description}
                    </p>
                  </div>
                </DashedPanel>
              ))}
            </div>
          </AnimateIn>
        </div>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>

      {/* ── The Team ── */}
      <Section>
        <div className="flex flex-col gap-8">
          <SectionHeader title="The Team" />
          <AnimateIn>
            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
              {TEAM_MEMBERS.map((member, i) => (
                <a
                  key={i}
                  href={member.url}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="block group"
                >
                  <DashedPanel withCorners className="p-5 sm:p-6 h-full hover:bg-white/[0.02] transition-colors">
                    <div className="flex flex-col items-center text-center gap-4">
                      <div className="w-20 h-20 rounded-full overflow-hidden border-2 border-border group-hover:border-fg/30 transition-colors">
                        <img
                          src={member.image}
                          alt={member.name}
                          className="w-full h-full object-cover"
                        />
                      </div>
                      <div className="flex flex-col gap-1">
                        <h3 className="font-display font-semibold text-fg text-sm group-hover:text-white transition-colors">
                          {member.name}
                        </h3>
                        <span
                          className="text-xs font-mono tracking-wider uppercase"
                          style={{ color: SILVER.mid }}
                        >
                          {member.role}
                        </span>
                      </div>
                    </div>
                  </DashedPanel>
                </a>
              ))}
            </div>
          </AnimateIn>
        </div>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>

      {/* ── Trust Metrics ── */}
      <Section>
        <div className="flex flex-col gap-8">
          <SectionHeader title="Trust Metrics" />
          <AnimateIn>
            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
              {TRUST_METRICS.map((metric) => (
                <DashedPanel
                  key={metric.label}
                  withCorners
                  className="p-5 sm:p-6"
                >
                  <div className="flex flex-col items-center text-center gap-3">
                    <div className="text-muted/40">{metric.icon}</div>
                    <span
                      className="text-2xl font-bold tabular-nums tracking-tight"
                      style={{
                        background: SILVER.gradient,
                        WebkitBackgroundClip: "text",
                        WebkitTextFillColor: "transparent",
                      }}
                    >
                      {metric.value}
                    </span>
                    <span className="text-xs font-mono text-muted/60 tracking-wider uppercase">
                      {metric.label}
                    </span>
                  </div>
                </DashedPanel>
              ))}
            </div>
          </AnimateIn>
        </div>
      </Section>

      <Section padding="none">
        <CrosshairDivider />
      </Section>

      {/* ── CTA ── */}
      <Section>
        <AnimateIn>
          <div className="flex flex-col items-center text-center gap-6">
            <h2 className="font-display font-bold text-2xl lg:text-3xl text-fg">
              Join the movement
            </h2>
            <p className="text-muted max-w-md">
              Whether you're investing in the future of decentralized
              infrastructure or want early access to the network, there's a
              place for you.
            </p>
            <div className="flex flex-col sm:flex-row gap-3">
              <Button asChild size="lg">
                <Link to="/investors">Become an Investor</Link>
              </Button>
              <Button asChild variant="ghost" size="lg">
                <a href="https://t.me/debrosportal" target="_blank" rel="noopener noreferrer">Join the Waitlist</a>
              </Button>
            </div>
          </div>
        </AnimateIn>
      </Section>

      {/* ── Floating Voice Player ── */}
      <FloatingVoicePlayer />
    </Page>
  );
}
