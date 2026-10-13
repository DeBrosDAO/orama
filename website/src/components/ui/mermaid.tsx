import { useEffect, useRef, useState } from "react";
import mermaid from "mermaid";

mermaid.initialize({
  startOnLoad: false,
  // Labels are sanitized and click handlers are off; pinned rather than left to the default.
  securityLevel: "strict",
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

type RenderFn = (id: string, chart: string) => Promise<{ svg: string }>;

/** The diagram's SVG, or the reason it could not be drawn: a bad chart never rejects past here. */
export async function renderDiagram(render: RenderFn, id: string, chart: string): Promise<{ svg: string; error: string }> {
  try {
    const { svg } = await render(id, chart);
    return { svg, error: "" };
  } catch (err: unknown) {
    return { svg: "", error: err instanceof Error ? err.message : String(err) };
  }
}

export function Mermaid({ chart }: { chart: string }) {
  const containerRef = useRef<HTMLDivElement>(null);
  const [svg, setSvg] = useState<string>("");
  const [error, setError] = useState<string>("");

  useEffect(() => {
    // A render that finishes after the chart changed is dropped.
    let current = true;
    const id = `mermaid-${++mermaidCounter}`;
    renderDiagram((i, c) => mermaid.render(i, c), id, chart).then((result) => {
      if (current) {
        setSvg(result.svg);
        setError(result.error);
      }
    });
    return () => {
      current = false;
    };
  }, [chart]);

  if (error) {
    return (
      <div role="alert" className="my-6 border border-dashed border-border rounded-sm p-4 bg-surface text-sm">
        Diagram could not be rendered: {error}
      </div>
    );
  }
  return (
    <div
      ref={containerRef}
      className="my-6 flex justify-center overflow-x-auto border border-dashed border-border rounded-sm p-4 bg-surface"
      dangerouslySetInnerHTML={{ __html: svg }}
    />
  );
}
