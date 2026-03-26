import { useState, useCallback } from "react";
import { Link, useLocation, useNavigate } from "react-router";
import * as DropdownMenu from "@radix-ui/react-dropdown-menu";
import { Search, Menu, X, ChevronDown, Code2, Server, GitBranch, Check } from "lucide-react";
import {
  PERSONA_DOCS,
  PERSONA_FIRST_SLUG,
} from "../../data/docs-navigation";
import type { DocLink } from "../../data/docs-navigation";
import type { Persona } from "../../types/persona";
import { cn } from "../../lib/utils";
import { SearchDialog } from "../ui/search-dialog";

/* -------------------------------------------------------------------------- */
/*  Helpers                                                                    */
/* -------------------------------------------------------------------------- */

const PERSONAS: {
  key: Persona;
  label: string;
  icon: typeof Code2;
  desc: string;
}[] = [
  { key: "developer", label: "Developers", icon: Code2, desc: "SDK, CLI, and API docs" },
  { key: "operator", label: "Operators", icon: Server, desc: "Node setup and monitoring" },
  { key: "contributor", label: "Contributors", icon: GitBranch, desc: "Source code and tooling" },
];

function getPersonaFromPath(pathname: string): Persona {
  if (pathname.startsWith("/docs/operator")) return "operator";
  if (pathname.startsWith("/docs/contributor")) return "contributor";
  return "developer";
}

/* -------------------------------------------------------------------------- */
/*  Persona select                                                             */
/* -------------------------------------------------------------------------- */

function PersonaSelect({
  persona,
  onSwitch,
}: {
  persona: Persona;
  onSwitch: (p: Persona) => void;
}) {
  const current = PERSONAS.find((p) => p.key === persona)!;
  const CurrentIcon = current.icon;

  return (
    <DropdownMenu.Root>
      <DropdownMenu.Trigger asChild>
        <button
          type="button"
          className="w-full flex items-center gap-2.5 px-3 py-2.5 rounded-md border border-border/50 bg-surface-2/40 hover:bg-surface-2/80 transition-colors outline-none group"
        >
          <CurrentIcon size={16} className="text-accent shrink-0" />
          <div className="flex-1 text-left min-w-0">
            <div className="text-sm text-fg font-medium leading-tight">
              {current.label}
            </div>
            <div className="text-[10px] text-muted leading-tight mt-0.5">
              {current.desc}
            </div>
          </div>
          <ChevronDown
            size={14}
            className="text-muted shrink-0 group-data-[state=open]:rotate-180 transition-transform"
          />
        </button>
      </DropdownMenu.Trigger>

      <DropdownMenu.Portal>
        <DropdownMenu.Content
          side="bottom"
          align="start"
          sideOffset={6}
          className="z-50 w-[var(--radix-dropdown-menu-trigger-width)] bg-surface border border-border/60 rounded-md p-1 shadow-[0_8px_32px_rgba(0,0,0,0.5)]"
        >
          {PERSONAS.map((p) => {
            const Icon = p.icon;
            const isActive = p.key === persona;
            return (
              <DropdownMenu.Item
                key={p.key}
                onSelect={() => onSwitch(p.key)}
                className={cn(
                  "flex items-center gap-2.5 px-3 py-2.5 rounded-sm cursor-pointer outline-none transition-colors",
                  isActive
                    ? "bg-accent/8"
                    : "hover:bg-surface-2",
                )}
              >
                <Icon
                  size={15}
                  className={cn(
                    "shrink-0",
                    isActive ? "text-accent" : "text-muted",
                  )}
                />
                <div className="flex-1 min-w-0">
                  <div
                    className={cn(
                      "text-sm leading-tight",
                      isActive ? "text-accent font-medium" : "text-fg",
                    )}
                  >
                    {p.label}
                  </div>
                  <div className="text-[10px] text-muted leading-tight mt-0.5">
                    {p.desc}
                  </div>
                </div>
                {isActive && (
                  <Check size={14} className="text-accent shrink-0" />
                )}
              </DropdownMenu.Item>
            );
          })}
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  );
}

/* -------------------------------------------------------------------------- */
/*  Nav link                                                                   */
/* -------------------------------------------------------------------------- */

function NavLink({
  link,
  isActive,
  onClick,
}: {
  link: DocLink;
  isActive: boolean;
  onClick?: () => void;
}) {
  const Icon = link.icon;

  return (
    <li>
      <Link
        to={`/docs/${link.slug}`}
        onClick={onClick}
        className={cn(
          "flex items-center gap-2.5 py-2 px-3 rounded-md transition-all duration-150 group",
          isActive
            ? "bg-accent/8 text-accent"
            : "text-muted hover:text-fg hover:bg-surface-2/60",
        )}
      >
        <Icon
          size={15}
          className={cn(
            "shrink-0 transition-colors",
            isActive ? "text-accent" : "text-muted/60 group-hover:text-fg/60",
          )}
        />
        <span className="text-[13px] leading-tight truncate">{link.title}</span>
      </Link>
    </li>
  );
}

/* -------------------------------------------------------------------------- */
/*  Sidebar content (shared between desktop and mobile)                        */
/* -------------------------------------------------------------------------- */

function SidebarContent({ onLinkClick }: { onLinkClick?: () => void }) {
  const { pathname } = useLocation();
  const navigate = useNavigate();
  const persona = getPersonaFromPath(pathname);
  const [searchOpen, setSearchOpen] = useState(false);

  const links = PERSONA_DOCS[persona];

  const handlePersonaSwitch = useCallback(
    (p: Persona) => {
      navigate(`/docs/${PERSONA_FIRST_SLUG[p]}`);
    },
    [navigate],
  );

  return (
    <>
      {/* Push below floating navbar */}
      <div className="pt-20 flex flex-col h-full">
        {/* Search trigger */}
        <div className="px-3 pb-3">
          <button
            type="button"
            onClick={() => setSearchOpen(true)}
            className="w-full flex items-center gap-2 py-2 px-3 text-xs text-muted hover:text-fg bg-surface-2/50 hover:bg-surface-2 border border-border/60 rounded-md transition-colors font-mono"
          >
            <Search size={13} className="shrink-0" />
            <span className="flex-1 text-left">Search docs...</span>
            <kbd className="hidden sm:inline text-[10px] text-muted/50 border border-border/40 rounded px-1.5 py-0.5">
              ⌘K
            </kbd>
          </button>
        </div>

        {/* Persona select */}
        <div className="px-3 pb-3">
          <PersonaSelect persona={persona} onSwitch={handlePersonaSwitch} />
        </div>

        {/* Divider */}
        <div className="border-t border-border/40 mx-3" />

        {/* Links */}
        <nav className="flex-1 overflow-y-auto px-2 py-3">
          <ul className="flex flex-col gap-0.5">
            {links.map((link) => (
              <NavLink
                key={link.slug}
                link={link}
                isActive={pathname === `/docs/${link.slug}`}
                onClick={onLinkClick}
              />
            ))}
          </ul>
        </nav>
      </div>

      {/* Search dialog */}
      <SearchDialog open={searchOpen} onOpenChange={setSearchOpen} />
    </>
  );
}

/* -------------------------------------------------------------------------- */
/*  Exported sidebar                                                           */
/* -------------------------------------------------------------------------- */

export function DocsSidebar() {
  const [mobileOpen, setMobileOpen] = useState(false);

  const handleLinkClick = useCallback(() => {
    setMobileOpen(false);
  }, []);

  return (
    <>
      {/* Mobile toggle button */}
      <button
        type="button"
        onClick={() => setMobileOpen((prev) => !prev)}
        className="lg:hidden fixed bottom-4 right-4 z-50 flex items-center justify-center w-12 h-12 bg-surface border border-dashed border-border rounded-full text-muted hover:text-fg transition-colors shadow-lg"
        aria-label={mobileOpen ? "Close sidebar" : "Open sidebar"}
      >
        {mobileOpen ? <X size={20} /> : <Menu size={20} />}
      </button>

      {/* Mobile overlay */}
      {mobileOpen && (
        <div
          className="lg:hidden fixed inset-0 z-40 bg-bg/80 backdrop-blur-sm"
          onClick={() => setMobileOpen(false)}
          onKeyDown={(e) => {
            if (e.key === "Escape") setMobileOpen(false);
          }}
          role="button"
          tabIndex={-1}
          aria-label="Close sidebar"
        />
      )}

      {/* Mobile drawer */}
      <aside
        className={cn(
          "lg:hidden fixed top-0 left-0 bottom-0 z-40 w-64 bg-surface border-r border-border/40 overflow-hidden transition-transform duration-200",
          mobileOpen ? "translate-x-0" : "-translate-x-full",
        )}
      >
        <SidebarContent onLinkClick={handleLinkClick} />
      </aside>

      {/* Desktop sidebar — fixed, full height */}
      <aside className="hidden lg:block fixed left-0 top-0 bottom-0 w-56 bg-surface border-r border-border/40 z-30">
        <SidebarContent />
      </aside>
    </>
  );
}
