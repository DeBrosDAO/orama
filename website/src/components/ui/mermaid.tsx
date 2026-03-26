import { useEffect, useRef, useState } from "react";
import mermaid from "mermaid";

mermaid.initialize({
  startOnLoad: false,
  theme: "dark",
  themeVariables: {
    darkMode: true,
    background: "#000000",
    primaryColor: "#1a1a2e",
    primaryTextColor: "#ffffff",
    primaryBorderColor: "#333333",
    lineColor: "#4169E1",
    secondaryColor: "#111111",
    tertiaryColor: "#0a0a0a",
    fontFamily: "DM Mono, monospace",
    fontSize: "14px",
    nodeBorder: "#333333",
    clusterBkg: "#111111",
    clusterBorder: "#333333",
    edgeLabelBackground: "#000000",
    nodeTextColor: "#ffffff",
  },
});

let mermaidCounter = 0;

export function Mermaid({ chart }: { chart: string }) {
  const containerRef = useRef<HTMLDivElement>(null);
  const [svg, setSvg] = useState<string>("");

  useEffect(() => {
    const id = `mermaid-${++mermaidCounter}`;
    mermaid.render(id, chart).then(({ svg: rendered }) => {
      setSvg(rendered);
    });
  }, [chart]);

  return (
    <div
      ref={containerRef}
      className="my-6 flex justify-center overflow-x-auto border border-dashed border-border rounded-sm p-4 bg-surface"
      dangerouslySetInnerHTML={{ __html: svg }}
    />
  );
}
