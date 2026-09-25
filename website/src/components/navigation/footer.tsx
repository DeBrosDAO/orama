import { Link } from "react-router";
import { Github } from "lucide-react";
import { CrosshairDivider } from "../ui/crosshair-divider";
import { DOCS_PATH, ROUTES } from "../../content/routes";
import { ANCHAT_GROUP_URL, CONTACT_EMAIL, GITHUB_URL, X_URL } from "../../content/site";
import { LICENSE } from "../../content/facts";
import oramaIcon from "../../assets/orama-icon.png";

interface FooterLink {
  label: string;
  to: string;
  external?: boolean;
}

const COLUMNS: { title: string; links: FooterLink[] }[] = [
  {
    title: "Explore",
    links: [
      { label: ROUTES.platform.nav, to: ROUTES.platform.path },
      { label: ROUTES.howItWorks.nav, to: ROUTES.howItWorks.path },
      { label: ROUTES.useCases.nav, to: ROUTES.useCases.path },
      { label: ROUTES.apps.title, to: ROUTES.apps.path },
    ],
  },
  {
    title: "Project",
    links: [
      { label: ROUTES.roadmap.nav, to: ROUTES.roadmap.path },
      { label: ROUTES.whitepaper.title, to: ROUTES.whitepaper.path },
      { label: ROUTES.investors.title, to: ROUTES.investors.path },
      { label: ROUTES.donate.title, to: ROUTES.donate.path },
    ],
  },
  {
    title: "Resources",
    links: [
      { label: "Docs", to: DOCS_PATH },
      { label: "GitHub", to: GITHUB_URL, external: true },
      { label: "X", to: X_URL, external: true },
      { label: "AnChat group", to: ANCHAT_GROUP_URL, external: true },
    ],
  },
];

const linkClass = "block py-1.5 text-sm text-muted hover:text-fg transition-colors";

/** The official X logo; lucide only ships the old bird. */
function XLogo({ size = 15 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
      <path d="M18.244 2.25h3.308l-7.227 8.26 8.502 11.24H16.17l-5.214-6.817L4.99 21.75H1.68l7.73-8.835L1.254 2.25H8.08l4.713 6.231zm-1.161 17.52h1.833L7.084 4.126H5.117z" />
    </svg>
  );
}

export function Footer() {
  return (
    <footer className="no-print">
      <CrosshairDivider />

      <div className="max-w-6xl mx-auto px-4 sm:px-6 py-16 sm:py-20">
        <div className="grid grid-cols-2 lg:grid-cols-5 gap-8">
          <div className="col-span-2 flex flex-col gap-4">
            <Link to="/" className="flex items-center gap-2.5">
              <img src={oramaIcon} alt="Orama" className="h-6 w-6" />
              <span className="font-display text-base font-bold tracking-widest text-fg">ORAMA</span>
            </Link>
            <p className="text-muted text-sm max-w-xs">The cloud, with nobody in the middle. Open source, built in the open.</p>
            <a href={`mailto:${CONTACT_EMAIL}`} className="font-mono text-xs text-accent hover:text-fg transition-colors w-fit">
              {CONTACT_EMAIL}
            </a>
          </div>

          {COLUMNS.map((column) => (
            <div key={column.title}>
              <h3 className="text-xs font-mono text-muted tracking-wider uppercase mb-4">{column.title}</h3>
              <ul>
                {column.links.map((link) => (
                  <li key={link.label}>
                    {link.external ? (
                      <a href={link.to} target="_blank" rel="noopener noreferrer" className={linkClass}>
                        {link.label}
                      </a>
                    ) : (
                      <Link to={link.to} className={linkClass}>
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

      <div className="border-t border-dashed border-border">
        <div className="max-w-6xl mx-auto px-4 sm:px-6 py-6 flex flex-col sm:flex-row items-center justify-between gap-4">
          <span className="text-xs font-mono text-muted tracking-wider">
            &copy; {__BUILD_YEAR__} Orama Network · {LICENSE}
          </span>
          <div className="flex items-center gap-4">
            <a href={GITHUB_URL} target="_blank" rel="noopener noreferrer" className="text-muted hover:text-fg transition-colors" aria-label="GitHub">
              <Github size={16} />
            </a>
            <a href={X_URL} target="_blank" rel="noopener noreferrer" className="text-muted hover:text-fg transition-colors" aria-label="X">
              <XLogo />
            </a>
            <a
              href={ANCHAT_GROUP_URL}
              target="_blank"
              rel="noopener noreferrer"
              className="opacity-50 hover:opacity-100 transition-opacity"
              aria-label="Orama group on AnChat"
            >
              <img src="/images/apps/anchat-mark.png" alt="" className="w-4 h-4" />
            </a>
          </div>
        </div>
      </div>
    </footer>
  );
}
