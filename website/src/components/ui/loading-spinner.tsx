import { cn } from "../../lib/utils";

export interface LoadingSpinnerProps {
  className?: string;
}

export function LoadingSpinner({ className }: LoadingSpinnerProps) {
  return (
    <div
      className={cn(
        "flex items-center justify-center w-full h-full min-h-32",
        className,
      )}
    >
      <svg
        width="32"
        height="32"
        viewBox="0 0 32 32"
        fill="none"
        className="text-accent animate-pulse"
      >
        {/* Vertical crosshair line */}
        <line
          x1="16"
          y1="0"
          x2="16"
          y2="32"
          stroke="currentColor"
          strokeWidth="1"
          strokeDasharray="2 2"
        />
        {/* Horizontal crosshair line */}
        <line
          x1="0"
          y1="16"
          x2="32"
          y2="16"
          stroke="currentColor"
          strokeWidth="1"
          strokeDasharray="2 2"
        />
        {/* Center circle */}
        <circle
          cx="16"
          cy="16"
          r="5"
          stroke="currentColor"
          strokeWidth="1"
        />
        {/* Inner dot */}
        <circle cx="16" cy="16" r="1.5" fill="currentColor" />
      </svg>
    </div>
  );
}
