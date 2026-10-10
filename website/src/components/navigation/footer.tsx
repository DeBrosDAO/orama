import { Link } from "react-router";
import { CrosshairDivider } from "../ui/crosshair-divider";
import { ROUTES } from "../../content/routes";
import { DOCS_PATH, EXPLORER_PATH } from "../../content/pages";
import { BLOG_FEED_PATH, BLOG_PATH } from "../../blog/posts";
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
    title: "Network",
    links: [
      { label: ROUTES.platform.title, to: ROUTES.platform.path },
      { label: ROUTES.howItWorks.title, to: ROUTES.howItWorks.path },
      { label: ROUTES.useCases.title, to: ROUTES.useCases.path },
      { label: ROUTES.apps.title, to: ROUTES.apps.path },
      { label: "Explorer", to: EXPLORER_PATH },
    ],
  },
  {
    title: "Project",
    links: [
      { label: ROUTES.roadmap.title, to: ROUTES.roadmap.path },
      { label: ROUTES.whitepaper.title, to: ROUTES.whitepaper.path },
      { label: "Blog", to: BLOG_PATH },
      { label: ROUTES.investors.title, to: ROUTES.investors.path },
      { label: ROUTES.donate.title, to: ROUTES.donate.path },
    ],
  },
  {
    title: "Resources",
    links: [
      { label: "Docs", to: DOCS_PATH },
      { label: "RSS feed", to: BLOG_FEED_PATH, external: true },
      { label: "GitHub", to: GITHUB_URL, external: true },
      { label: "X", to: X_URL, external: true },
      { label: "AnChat group", to: ANCHAT_GROUP_URL, external: true },
    ],
  },
];

const linkClass = "block py-1.5 text-sm text-muted hover:text-fg transition-colors";

/** The GitHub mark; lucide 1.x dropped its brand icons. */
function GithubLogo({ size = 16 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 16 16" fill="currentColor" aria-hidden="true">
      <path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0016 8c0-4.42-3.58-8-8-8z" />
    </svg>
  );
}

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
            <a
              href={ANCHAT_GROUP_URL}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-2 font-mono text-xs text-muted hover:text-fg transition-colors w-fit"
            >
              <img src="/images/apps/anchat-mark.png" alt="" className="w-3.5 h-3.5" />
              Join the Orama group on AnChat
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
              <GithubLogo />
            </a>
            <a href={X_URL} target="_blank" rel="noopener noreferrer" className="text-muted hover:text-fg transition-colors" aria-label="X">
              <XLogo />
            </a>
            <a
              href={ANCHAT_GROUP_URL}
              target="_blank"
              rel="noopener noreferrer"
              className="opacity-70 hover:opacity-100 transition-opacity"
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
