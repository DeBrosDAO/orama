import { useId, useState } from "react";
import type { FormEvent } from "react";
import { useNavigate } from "react-router";

export interface LookupFormProps {
  /** What is being looked up, for the screen reader: "Token denom". */
  label: string;
  placeholder: string;
  /** The page the text names, or null when it names none. Runs on every keystroke, so it must be cheap and ask no server. */
  resolve: (text: string) => string | null;
  /** Said under the box when the text names nothing. */
  hint: string;
  className?: string;
}

/** A box that opens the page a pasted identifier names. It checks the text's shape only; the page it opens says whether the chain has it. */
export function LookupForm({ label, placeholder, resolve, hint, className }: LookupFormProps) {
  const navigate = useNavigate();
  const inputId = useId();
  const hintId = useId();
  const [text, setText] = useState("");
  const target = resolve(text.trim());
  const invalid = text.trim() !== "" && target === null;

  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (target !== null) void navigate(target);
  };

  return (
    <form onSubmit={submit} className={className}>
      <div className="flex items-center gap-2">
        <label htmlFor={inputId} className="sr-only">
          {label}
        </label>
        <input
          id={inputId}
          autoComplete="off"
          spellCheck={false}
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder={placeholder}
          aria-invalid={invalid}
          aria-describedby={invalid ? hintId : undefined}
          className="min-w-0 flex-1 rounded-lg border border-border bg-bg px-3 py-1.5 font-mono text-sm placeholder:font-sans placeholder:text-muted aria-[invalid=true]:border-loss"
        />
        <button
          type="submit"
          disabled={target === null}
          className="rounded-lg border border-border bg-surface-2 px-3 py-1.5 text-sm enabled:hover:border-fg/30 enabled:cursor-pointer disabled:opacity-50"
        >
          Go
        </button>
      </div>
      {invalid && (
        <p id={hintId} role="status" className="mt-1.5 text-xs text-loss">
          {hint}
        </p>
      )}
    </form>
  );
}
