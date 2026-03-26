/**
 * Redacted / "Coming Soon" component for hiding sensitive tokenomics data.
 * Renders asterisks with a blur effect that cannot be defeated by CSS overrides,
 * because the actual text content is replaced — there's nothing to unblur.
 */

interface RedactedProps {
  /** Optional label shown on hover */
  label?: string;
  /** How many asterisks to show (default 3) */
  length?: number;
  /** Display as block-level "Coming Soon" banner instead of inline */
  block?: boolean;
  className?: string;
}

export function Redacted({ label = "Coming Soon", length = 3, block, className = "" }: RedactedProps) {
  const stars = "***".repeat(Math.max(1, Math.ceil(length / 3)));

  if (block) {
    return (
      <div className={`redacted-block ${className}`} title={label}>
        <span className="redacted-stars">{stars}</span>
        <span className="redacted-label">{label}</span>
      </div>
    );
  }

  return (
    <span className={`redacted-inline ${className}`} title={label}>
      {stars}
    </span>
  );
}

/**
 * Wraps a section with a "Coming Soon" overlay.
 * The children are blurred and a centered label is shown on top.
 */
export function ComingSoonOverlay({ children, label = "Coming Soon", className = "" }: {
  children: React.ReactNode;
  label?: string;
  className?: string;
}) {
  return (
    <div className={`coming-soon-overlay ${className}`}>
      <div className="coming-soon-content" aria-hidden="true">
        {children}
      </div>
      <div className="coming-soon-badge">
        <span>{label}</span>
      </div>
    </div>
  );
}
