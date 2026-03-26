import type { ReactNode } from "react";
import { cn } from "../../lib/utils";

export interface FeatureCardProps {
  icon: ReactNode;
  title: string;
  description: string;
  subtitle?: string;
  href?: string;
  className?: string;
}

export function FeatureCard({
  icon,
  title,
  description,
  subtitle,
  href,
  className,
}: FeatureCardProps) {
  const content = (
    <>
      <div className="text-accent mb-4">{icon}</div>
      <h3 className="font-display font-semibold text-fg mb-1">{title}</h3>
      {subtitle && (
        <p className="text-xs font-mono text-muted/60 mb-2">{subtitle}</p>
      )}
      <p className="text-sm text-muted leading-relaxed">{description}</p>
    </>
  );

  const sharedClasses = cn(
    "block border border-dashed border-border p-4 sm:p-6 transition-all duration-200",
    "hover:bg-surface-2/50 hover:border-border/80 hover:shadow-[0_4px_16px_rgba(0,0,0,0.3)] hover:-translate-y-0.5",
    className,
  );

  if (href) {
    return (
      <a href={href} className={sharedClasses}>
        {content}
      </a>
    );
  }

  return <div className={sharedClasses}>{content}</div>;
}
