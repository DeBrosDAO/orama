import { useId, useState } from "react";
import type { FormEvent } from "react";
import { useNavigate } from "react-router";
import { ChainReadError } from "../data/chain/client";
import { useNetwork } from "../network/active";
import { customHome, switchTargets } from "../network/selection";
import { confirmGateway } from "../network/gate";

/** The select's value for "type a gateway"; not a network name, so no registry network can collide with it. */
const CUSTOM_CHOICE = "Custom gateway…";

export const GATEWAY_EXAMPLE = "https://gateway.example.org";

/**
 * The network selector: the registry's created networks and a gateway the
 * reader types. Choosing a network opens its front page, because a page of one
 * chain does not exist on another. A typed gateway is checked with chainBase
 * (https, or http on this machine) before anything is requested from it.
 */
export function NetworkSwitcher() {
  const network = useNetwork();
  const navigate = useNavigate();
  const selectId = useId();
  const inputId = useId();
  const errorId = useId();
  const [typing, setTyping] = useState(false);
  const [gateway, setGateway] = useState(network.gateway ?? "");
  const [typedError, setError] = useState<string | null>(null);
  const error = typedError ?? network.problem;
  const showForm = typing || network.custom;

  function choose(value: string) {
    if (value === CUSTOM_CHOICE) {
      setTyping(true);
      return;
    }
    setTyping(false);
    setError(null);
    const target = switchTargets(network.registry).find((t) => t.key === value);
    if (target) navigate(target.to);
  }

  function submit(e: FormEvent) {
    e.preventDefault();
    const typed = gateway.trim();
    try {
      const home = customHome(typed);
      confirmGateway(typed);
      navigate(home);
      setError(null);
      setTyping(false);
    } catch (err) {
      setError(err instanceof ChainReadError ? err.message : "That is not a gateway address.");
    }
  }

  return (
    <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1.5">
      <label htmlFor={selectId} className="text-muted">
        Network
      </label>
      <select
        id={selectId}
        value={showForm ? CUSTOM_CHOICE : network.key}
        onChange={(e) => choose(e.target.value)}
        className="cursor-pointer rounded-md border border-border bg-bg px-2 py-1 text-fg hover:border-fg/30"
      >
        {switchTargets(network.registry).map((t) => (
          <option key={t.key} value={t.key}>
            {t.label}
          </option>
        ))}
        <option value={CUSTOM_CHOICE}>{CUSTOM_CHOICE}</option>
      </select>
      {showForm && (
        <form onSubmit={submit} noValidate className="flex min-w-0 flex-wrap items-center gap-2" aria-label="Read from a gateway you name">
          <label htmlFor={inputId} className="sr-only">
            Gateway address
          </label>
          <input
            id={inputId}
            type="url"
            inputMode="url"
            autoComplete="off"
            spellCheck={false}
            value={gateway}
            onChange={(e) => setGateway(e.target.value)}
            placeholder={GATEWAY_EXAMPLE}
            aria-invalid={error !== null}
            aria-describedby={error ? errorId : undefined}
            className="w-64 max-w-full rounded-md border border-border bg-bg px-2 py-1 font-mono text-fg placeholder:text-muted"
          />
          <button type="submit" className="cursor-pointer rounded-md border border-border bg-surface-2 px-2.5 py-1 hover:border-fg/30">
            Read this gateway
          </button>
          {error && (
            <span id={errorId} role="alert" className="text-loss">
              {error}
            </span>
          )}
        </form>
      )}
    </div>
  );
}
