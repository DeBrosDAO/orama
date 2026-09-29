import type { ReactNode } from "react";
import { Card } from "./card";

export interface StatProps {
  label: ReactNode;
  value: ReactNode;
  detail?: ReactNode;
  /** A trend line or a progress bar under the numbers. */
  footer?: ReactNode;
}

export function Stat({ label, value, detail, footer }: StatProps) {
  return (
    <Card title={label}>
      <div className="text-2xl font-semibold tracking-tight tabular-nums">{value}</div>
      {detail && <div className="mt-0.5 text-[13px] text-muted">{detail}</div>}
      {footer && <div className="mt-2 text-muted">{footer}</div>}
    </Card>
  );
}
