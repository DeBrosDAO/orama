import type { SVGProps } from "react";

export function ReactLogo(props: SVGProps<SVGSVGElement>) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1"
      width="24"
      height="24"
      {...props}
    >
      {/* Center dot */}
      <circle cx="12" cy="12" r="1.5" fill="currentColor" stroke="none" />
      {/* Orbit 1 — horizontal */}
      <ellipse cx="12" cy="12" rx="10" ry="4" />
      {/* Orbit 2 — tilted right */}
      <ellipse cx="12" cy="12" rx="10" ry="4" transform="rotate(60 12 12)" />
      {/* Orbit 3 — tilted left */}
      <ellipse cx="12" cy="12" rx="10" ry="4" transform="rotate(120 12 12)" />
    </svg>
  );
}

export function NextjsLogo(props: SVGProps<SVGSVGElement>) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="currentColor"
      width="24"
      height="24"
      {...props}
    >
      {/* "N" lettermark */}
      <path d="M5 4v16h2.5V8.5L18.2 21.6l1.8-1.2V4h-2.5v11.5L6.8 2.4 5 4z" />
    </svg>
  );
}

export function GoLogo(props: SVGProps<SVGSVGElement>) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="currentColor"
      width="24"
      height="24"
      {...props}
    >
      {/* Stylized "Go" text */}
      <text
        x="12"
        y="16"
        textAnchor="middle"
        fontSize="14"
        fontWeight="700"
        fontFamily="sans-serif"
        fill="currentColor"
      >
        Go
      </text>
    </svg>
  );
}

export function NodejsLogo(props: SVGProps<SVGSVGElement>) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.2"
      strokeLinejoin="round"
      width="24"
      height="24"
      {...props}
    >
      {/* Hexagon shape */}
      <path d="M12 2l8.66 5v10L12 22l-8.66-5V7L12 2z" />
      {/* Inner "N" mark */}
      <path d="M9 15V9l3 4 3-4v6" strokeLinecap="round" />
    </svg>
  );
}

export function WasmLogo(props: SVGProps<SVGSVGElement>) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="currentColor"
      width="24"
      height="24"
      {...props}
    >
      {/* WASM diamond/gear shape */}
      <path d="M12 1L22 7v10l-10 6L2 17V7l10-6z" fill="none" stroke="currentColor" strokeWidth="1.2" />
      {/* "W" inside */}
      <path d="M7 9l1.5 6 1.5-4 1.5 4 1.5-6" fill="none" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" strokeLinejoin="round" />
      {/* Small dot accent */}
      <circle cx="17" cy="12" r="1.2" fill="currentColor" stroke="none" />
    </svg>
  );
}
