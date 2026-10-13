import type { MessageField, TxMessage } from "../../model/types";
import { cn } from "../../../lib/utils";
import { Amount } from "../../ui/amount";

function FieldValue({ field }: { field: MessageField }) {
  if (field.kind === "norama") return <Amount norama={field.value} />;
  return <span className={field.kind === "address" ? "font-mono text-xs" : undefined}>{field.value}</span>;
}

/**
 * Labelled facts, one row each. Every value is chain-written text that a decoder
 * already cleaned and bounded; it is rendered as text, never as markup.
 */
export function FieldList({ fields, className }: { fields: readonly MessageField[]; className?: string }) {
  if (fields.length === 0) return null;
  return (
    <dl className={cn("grid grid-cols-[minmax(0,9rem)_minmax(0,1fr)] gap-x-3 gap-y-0.5 text-xs", className)}>
      {fields.map((field) => (
        <div key={field.label} className="contents">
          <dt className="text-muted">{field.label}</dt>
          <dd className="m-0 [overflow-wrap:anywhere]">
            <FieldValue field={field} />
          </dd>
        </div>
      ))}
    </dl>
  );
}

/** The structured facts of an action message, under its sentence. */
export function MessageFields({ message }: { message: TxMessage }) {
  if (message.type !== "action") return null;
  return <FieldList fields={message.fields} className="mt-1" />;
}
