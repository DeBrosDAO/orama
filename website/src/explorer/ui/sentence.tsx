import type { ReactNode } from "react";
import { describeMessage } from "../model/describe";
import type { SentencePart } from "../model/describe";
import type { TxMessage } from "../model/types";
import { Amount } from "./amount";
import { ValidatorLink, WalletLink } from "./links";

function renderPart(part: SentencePart, i: number): ReactNode {
  switch (part.kind) {
    case "text":
      return <span key={i}>{part.text}</span>;
    case "wallet":
      return <WalletLink key={i} wallet={part.ref} />;
    case "validator":
      return <ValidatorLink key={i} validator={part.ref} />;
    case "amount":
      return (
        <b key={i} className="font-semibold text-fg">
          <Amount norama={part.norama} className="font-sans tabular-nums" />
        </b>
      );
  }
}

/** Sentence parts as running text, with wallet and validator names as links. */
export function SentenceParts({ parts }: { parts: SentencePart[] }) {
  return <>{parts.map(renderPart)}</>;
}

/** "Alice sent 12.5 ORAMA to Bob", with the names as links. */
export function Sentence({ message, failed = false }: { message: TxMessage; failed?: boolean }) {
  return (
    <span className="leading-relaxed">
      <SentenceParts parts={describeMessage(message, failed)} />
    </span>
  );
}
