import { Link } from "react-router";
import { Github, Twitter, Send as SendIcon, Youtube } from "lucide-react";
import type { ReactNode } from "react";
import { FOOTER_COLUMNS, SOCIAL_LINKS } from "../../data/navigation";
import { CrosshairDivider } from "../ui/crosshair-divider";
import oramaIcon from "../../assets/orama-icon.png";

const CONTACT_EMAIL = "dev@debros.io";

const SOCIAL_ICON_MAP: Record<string, ReactNode> = {
  github: <Github size={16} />,
  twitter: <Twitter size={16} />,
  send: <SendIcon size={16} />,
  youtube: <Youtube size={16} />,
};

export function Footer() {
  return (
    <footer>
      <CrosshairDivider />

      <div className="max-w-6xl mx-auto px-4 sm:px-6 py-16 sm:py-20">
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-6 lg:gap-8">
          {/* Brand column */}
          <div className="sm:col-span-2 lg:col-span-1">
            <Link to="/" className="flex items-center gap-2.5 mb-4">
              <img src={oramaIcon} alt="Orama" className="h-6 w-6" />
              <span className="font-display text-base font-bold tracking-widest text-fg">
                ORAMA
              </span>
            </Link>
            <p className="text-muted text-sm leading-relaxed max-w-xs">
              The decentralized, privacy-first alternative to the big clouds.
              SQL, storage, cache, pubsub, functions and deployments, on a mesh
              of independently operated nodes.
            </p>
            <a
              href={`mailto:${CONTACT_EMAIL}`}
              className="inline-block mt-4 text-xs font-mono text-muted hover:text-fg transition-colors"
            >
              {CONTACT_EMAIL}
            </a>
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
