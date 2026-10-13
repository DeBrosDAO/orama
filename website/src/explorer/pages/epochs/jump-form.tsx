import { useState } from "react";
import type { FormEvent } from "react";
import { useNavigate } from "react-router";
import { useExplorerPaths } from "../../network/active";
import { parseEpochParam } from "./logic";

/** "Go to epoch": opens an epoch's page by number. */
export function JumpForm() {
  const explorerPaths = useExplorerPaths();
  const navigate = useNavigate();
  const [text, setText] = useState("");
  const epoch = parseEpochParam(text.trim());
  const invalid = text.trim() !== "" && epoch === null;

  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (epoch !== null) void navigate(explorerPaths.epoch(epoch));
  };

  return (
    <form onSubmit={submit} className="flex items-center gap-2">
      <label htmlFor="epoch-jump" className="sr-only">
        Epoch number
      </label>
      <input
        id="epoch-jump"
        inputMode="numeric"
        autoComplete="off"
        value={text}
        onChange={(e) => setText(e.target.value)}
        placeholder="Go to epoch…"
        aria-invalid={invalid}
        className="w-36 rounded-lg border border-border bg-bg px-3 py-1.5 text-sm placeholder:text-muted aria-[invalid=true]:border-loss"
      />
      <button
        type="submit"
        disabled={epoch === null}
        className="rounded-lg border border-border bg-surface-2 px-3 py-1.5 text-sm enabled:hover:border-fg/30 enabled:cursor-pointer disabled:opacity-50"
      >
        Go
      </button>
    </form>
  );
}
