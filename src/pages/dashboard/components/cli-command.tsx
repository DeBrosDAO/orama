import { useState } from "react";
import { TerminalSquare, Copy, Check } from "lucide-react";
import { Terminal } from "../../../components/ui/terminal";
import type { TerminalLine } from "../../../components/ui/terminal";

interface CliCommand {
  description: string;
  command: string;
}

interface CliCommandProps {
  commands: CliCommand[];
}

export function CliCommandDisplay({ commands }: CliCommandProps) {
  const [open, setOpen] = useState(false);
  const [copied, setCopied] = useState(false);

  const lines: TerminalLine[] = commands.flatMap((cmd, i) => {
    const result: TerminalLine[] = [];
    if (i > 0) result.push({ text: "" });
    result.push({ text: `# ${cmd.description}`, className: "opacity-50" });
    result.push({ prefix: "$", text: cmd.command });
    return result;
  });

  const handleCopy = () => {
    const text = commands.map((c) => c.command).join("\n");
    navigator.clipboard.writeText(text);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  return (
    <div className="flex flex-col gap-2">
      <button
        type="button"
        onClick={() => setOpen(!open)}
        className="flex items-center gap-1.5 font-mono text-[10px] text-muted uppercase tracking-wider hover:text-fg transition-colors self-start"
      >
        <TerminalSquare size={12} />
        CLI Equivalent
      </button>
      {open && (
        <div className="relative">
          <Terminal lines={lines} />
          <button
            type="button"
            onClick={handleCopy}
            className="absolute top-2.5 right-3 text-muted hover:text-fg transition-colors"
            title="Copy commands"
          >
            {copied ? <Check size={14} /> : <Copy size={14} />}
          </button>
        </div>
      )}
    </div>
  );
}
