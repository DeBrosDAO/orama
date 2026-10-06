import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';
import { describe, expect, it } from 'vitest';

/**
 * One place in the SDK is allowed to reach the console, and it is gated on the
 * client's `debug` flag. Nothing else may print.
 *
 * Without this guard the rule survives exactly until the next debugging
 * session: `console.log` is one line, it works, and it ships. The audit that
 * produced this change found eleven of them across five files, several of them
 * on the hot path — a line per HTTP request and a line per pubsub message
 * delivered, in every application that imported the SDK.
 */

const SRC = join(__dirname, '../../../src');

/** The logger itself. It is the console's only caller by design. */
const ALLOWED = ['core/logger.ts'];

function sourceFiles(dir: string): string[] {
  return readdirSync(dir).flatMap((entry) => {
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) return sourceFiles(full);
    return full.endsWith('.ts') ? [full] : [];
  });
}

/**
 * Strip comments so a `console.log` shown inside a documentation example — the
 * SDK's JSDoc shows callers what to do with a result — is not mistaken for the
 * SDK printing.
 */
function stripComments(source: string): string {
  return source.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '');
}

describe('the SDK prints only through its debug-gated logger', () => {
  const files = sourceFiles(SRC);

  it('finds the source tree', () => {
    expect(files.length).toBeGreaterThan(20);
  });

  it('has no console call outside the logger', () => {
    const offenders: string[] = [];

    for (const file of files) {
      const rel = relative(SRC, file).split('\\').join('/');
      if (ALLOWED.includes(rel)) continue;

      const code = stripComments(readFileSync(file, 'utf8'));
      const lines = code.split('\n');
      lines.forEach((line, index) => {
        if (/\bconsole\s*\./.test(line)) {
          offenders.push(`${rel}:${index + 1}: ${line.trim()}`);
        }
      });
    }

    expect(offenders, 'route these through the Logger from src/core/logger.ts').toEqual([]);
  });

  it('still sees console calls that are real code, not comments', () => {
    // Proves the comment stripping above has not made the check vacuous.
    const withComment = stripComments('/* console.log("doc") */\nconsole.log("real");');
    expect(/\bconsole\s*\./.test(withComment)).toBe(true);
    expect(withComment).not.toContain('doc');
  });
});
