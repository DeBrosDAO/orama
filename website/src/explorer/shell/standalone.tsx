import type { ReactNode } from "react";
import { Link, useLocation, useNavigate } from "react-router";
import { useNetwork } from "../network/active";
import { confirmGateway } from "../network/gate";
import { explorerPathsFor } from "../model/routes";
import { Card } from "../ui/card";
import { Page } from "../ui/page";
import oramaIcon from "../../assets/orama-icon.png";
import { NetworkSwitcher } from "./network-switcher";
import { MAIN_FOCUS, MAIN_ID, SkipLink } from "./skip-link";

/** The explorer's frame for a page that has no source to read yet: a custom gateway nobody has named, or not yet agreed to. */
function Frame({ children }: { children: ReactNode }) {
  return (
    <div className="explorer min-h-screen bg-bg text-fg">
      <SkipLink />
      <header className="border-b border-border bg-surface/90">
        <div className="mx-auto flex h-14 max-w-[1140px] items-center gap-3 px-4 sm:px-6">
          <Link to="/" className="flex shrink-0 items-center gap-2" aria-label="Orama home">
            <img src={oramaIcon} alt="" className="h-6 w-6" />
            <span className="font-display text-sm font-bold tracking-[0.14em]">ORAMA</span>
          </Link>
          <span className="text-sm text-muted">Explorer</span>
        </div>
      </header>
      <div className="border-b border-border bg-surface/60 text-xs">
        <div className="mx-auto max-w-[1140px] px-4 py-2 sm:px-6">
          <NetworkSwitcher />
        </div>
      </div>
      <main id={MAIN_ID} tabIndex={-1} className={MAIN_FOCUS}>
        <Page>{children}</Page>
      </main>
    </div>
  );
}

/** A custom network with no gateway in its link, or one that is refused: say what to type. */
export function NeedsGateway() {
  const { problem } = useNetwork();
  return (
    <Frame>
      <Card>
        <div className="space-y-2 py-4">
          <h1 className="font-display text-2xl font-semibold">Read a gateway you name</h1>
          <p className="max-w-prose text-sm text-muted">
            Type the https address of a gateway of an Orama network above. The explorer asks only its public chain routes, sends none of
            your cookies or credentials, and shows what that gateway answers.
          </p>
          {problem && (
            <p role="alert" className="text-sm text-loss">
              {problem}
            </p>
          )}
        </div>
      </Card>
    </Frame>
  );
}

/**
 * A link names a gateway the registry does not list. Opening it would make this
 * browser call that server, so nothing is requested until the reader agrees.
 */
export function ConfirmGateway({ gateway }: { gateway: string }) {
  const location = useLocation();
  const navigate = useNavigate();
  const { registry } = useNetwork();
  return (
    <Frame>
      <Card>
        <div className="space-y-3 py-4">
          <h1 className="font-display text-2xl font-semibold">This link reads from a gateway Orama does not list</h1>
          <p className="max-w-prose text-sm text-muted">
            The link asks the explorer to read the chain from{" "}
            <code className="break-all rounded bg-surface-3 px-1.5 py-0.5 font-mono text-fg">{gateway}</code>. Orama&apos;s network registry does
            not vouch for it: whoever runs it decides every figure shown, and it will see your address and the time you asked. The explorer
            sends it no cookies or credentials.
          </p>
          <div className="flex flex-wrap gap-2">
            <button
              type="button"
              onClick={() => {
                confirmGateway(gateway);
                navigate({ pathname: location.pathname, search: location.search }, { replace: true });
              }}
              className="cursor-pointer rounded-lg border border-border bg-surface-2 px-3 py-1.5 text-sm hover:border-fg/30"
            >
              Read from this gateway
            </button>
            <Link
              to={explorerPathsFor(null).home}
              className="rounded-lg border border-border px-3 py-1.5 text-sm text-muted hover:border-fg/30 hover:text-fg"
            >
              Open {registry.defaultName} instead
            </Link>
          </div>
        </div>
      </Card>
    </Frame>
  );
}
