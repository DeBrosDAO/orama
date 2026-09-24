import { useMemo } from "react";
import QRCode from "qrcode";

export interface QrCodeProps {
  value: string;
  label: string;
  size?: number;
}

/**
 * QR code drawn as SVG, generated in the browser (and at prerender time).
 * Nothing leaves the page: wallet addresses are never sent to a QR service.
 */
export function QrCode({ value, label, size = 160 }: QrCodeProps) {
  const { count, path } = useMemo(() => {
    const qr = QRCode.create(value, { errorCorrectionLevel: "M" });
    const n = qr.modules.size;
    let d = "";
    for (let y = 0; y < n; y++) {
      for (let x = 0; x < n; x++) {
        if (qr.modules.get(x, y)) d += `M${x} ${y}h1v1h-1z`;
      }
    }
    return { count: n, path: d };
  }, [value]);

  const quiet = 2;
  const box = count + quiet * 2;

  return (
    <svg
      role="img"
      aria-label={label}
      width={size}
      height={size}
      viewBox={`${-quiet} ${-quiet} ${box} ${box}`}
      shapeRendering="crispEdges"
      className="rounded-sm"
    >
      <rect x={-quiet} y={-quiet} width={box} height={box} fill="#fff" />
      <path d={path} fill="#000" />
    </svg>
  );
}
