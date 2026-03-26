import { useState } from "react";
import { Link } from "react-router";
import { Github, Twitter, Send as SendIcon, Youtube, Copy, Check, ArrowRight } from "lucide-react";
import type { ReactNode } from "react";
import { FOOTER_COLUMNS, SOCIAL_LINKS } from "../../data/navigation";
import { CrosshairDivider } from "../ui/crosshair-divider";
import oramaIcon from "../../assets/orama-icon.png";

const DONATE_WALLETS = [
  { chain: "BTC", address: "bc1qzpkjguxh4pl9pdhj76zeztur42prhfed2hd22z" },
];

const SOCIAL_ICON_MAP: Record<string, ReactNode> = {
  github: <Github size={16} />,
  twitter: <Twitter size={16} />,
  send: <SendIcon size={16} />,
  youtube: <Youtube size={16} />,
};

function FooterContact() {
  const [submitted, setSubmitted] = useState(false);
  const [email, setEmail] = useState("");
  const [message, setMessage] = useState("");

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!email.trim() || !message.trim()) return;

    try {
      await fetch("https://api.web3forms.com/submit", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          access_key: "YOUR_WEB3FORMS_KEY", // TODO: Replace with actual key
          to: "dev@debros.io",
          subject: "Orama Network — Contact Form",
          email,
          message,
        }),
      });
      setSubmitted(true);
    } catch {
      // Silently fail — user sees no change
    }
  };

  return (
    <div className="border-t border-dashed border-border">
      <div className="max-w-6xl mx-auto px-4 sm:px-6 py-8">
        <div className="max-w-lg">
          <h3 className="text-xs font-mono text-muted tracking-wider uppercase mb-1">Contact Us</h3>
          <p className="text-xs text-muted mb-4">Partnership inquiries, questions, or feedback.</p>

          {submitted ? (
            <p className="text-xs font-mono text-fg">Message sent. We'll get back to you soon.</p>
          ) : (
            <form onSubmit={handleSubmit} className="flex flex-col gap-3">
              <input
                type="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                placeholder="Your email"
                required
                className="w-full px-3 py-2 bg-transparent border border-dashed border-border text-fg text-xs font-mono placeholder:text-muted/40 focus:outline-none focus:border-fg/30 transition-colors"
              />
              <textarea
                value={message}
                onChange={(e) => setMessage(e.target.value)}
                placeholder="Your message..."
                rows={3}
                required
                className="w-full px-3 py-2 bg-transparent border border-dashed border-border text-fg text-xs font-mono placeholder:text-muted/40 focus:outline-none focus:border-fg/30 transition-colors resize-none"
              />
              <button
                type="submit"
                className="flex items-center justify-center gap-2 px-4 py-2 text-xs font-mono tracking-wider uppercase text-muted hover:text-fg border border-dashed border-border hover:border-fg/30 transition-all cursor-pointer w-fit"
              >
                Send
                <ArrowRight className="w-3 h-3" />
              </button>
            </form>
          )}
        </div>
      </div>
    </div>
  );
}

function FooterDonate() {
  const [copiedIdx, setCopiedIdx] = useState<number | null>(null);

  const handleCopy = (address: string, idx: number) => {
    navigator.clipboard.writeText(address);
    setCopiedIdx(idx);
    setTimeout(() => setCopiedIdx(null), 2000);
  };

  return (
    <div className="border-t border-dashed border-border">
      <div className="max-w-6xl mx-auto px-4 sm:px-6 py-8">
        <h3 className="text-xs font-mono text-muted tracking-wider uppercase mb-1">
          Support the Cause
        </h3>
        <p className="text-xs text-muted mb-4">
          Donate directly to support Orama Network development.
        </p>
        <div className="grid grid-cols-1 md:grid-cols-3 gap-3">
          {DONATE_WALLETS.map((w, i) => (
            <div
              key={w.chain}
              className="flex items-center justify-between gap-2 px-3 py-2 border border-dashed border-border hover:border-fg/20 transition-colors"
            >
              <div className="flex items-center gap-2 min-w-0">
                <span className="text-[10px] font-mono font-bold tracking-wider text-muted w-8 shrink-0">{w.chain}</span>
                <span className="text-[11px] font-mono text-fg truncate">{w.address}</span>
              </div>
              <button
                type="button"
                onClick={() => handleCopy(w.address, i)}
                className="text-muted hover:text-fg transition-colors cursor-pointer shrink-0"
              >
                {copiedIdx === i ? <Check size={14} /> : <Copy size={14} />}
              </button>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}

export function Footer() {
  return (
    <footer>
      <CrosshairDivider />

      {/* Main footer content */}
      <div className="max-w-6xl mx-auto px-4 sm:px-6 py-16 sm:py-20">
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-6 lg:gap-8">
          {/* Brand column */}
          <div className="md:col-span-2 lg:col-span-1">
            <Link to="/" className="flex items-center gap-2.5 mb-4">
              <img src={oramaIcon} alt="Orama" className="h-6 w-6" />
              <span className="font-display text-base font-bold tracking-widest text-fg">
                ORAMA
              </span>
            </Link>
            <p className="text-muted text-sm leading-relaxed max-w-xs">
              Decentralized cloud infrastructure. Deploy, store, and compute
              without centralized providers.
            </p>
          </div>

          {/* Link columns */}
          {FOOTER_COLUMNS.map((column) => (
            <div key={column.title}>
              <h3 className="text-xs font-mono text-muted tracking-wider uppercase mb-4">
                {column.title}
              </h3>
              <ul className="space-y-0.5">
                {column.links.map((link) => (
                  <li key={link.label}>
                    {link.external ? (
                      <a
                        href={link.href}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="block py-1.5 text-sm text-muted hover:text-fg transition-all duration-150 hover:translate-x-0.5"
                      >
                        {link.label}
                      </a>
                    ) : (
                      <Link
                        to={link.href}
                        className="block py-1.5 text-sm text-muted hover:text-fg transition-all duration-150 hover:translate-x-0.5"
                      >
                        {link.label}
                      </Link>
                    )}
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </div>
      </div>

      {/* Contact form */}
      <FooterContact />

      {/* Donate section */}
      <FooterDonate />

      {/* Bottom bar */}
      <div className="border-t border-dashed border-border">
        <div className="max-w-6xl mx-auto px-4 sm:px-6 py-6 flex flex-col sm:flex-row items-center justify-between gap-4">
          <span className="text-xs font-mono text-muted tracking-wider">
            &copy; {new Date().getFullYear()} Orama Network &mdash; DeBros DAO
          </span>
          <div className="flex items-center gap-4">
            {SOCIAL_LINKS.map((social) => (
              <a
                key={social.label}
                href={social.href}
                target="_blank"
                rel="noopener noreferrer"
                className="text-muted hover:text-fg transition-colors"
                aria-label={social.label}
              >
                {SOCIAL_ICON_MAP[social.icon]}
              </a>
            ))}
          </div>
        </div>
      </div>
    </footer>
  );
}
