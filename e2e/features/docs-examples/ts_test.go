//go:build e2e_fleet

package docsexamples

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/cliconf"
	"github.com/DeBrosOfficial/network/e2e/harness"
)

// tsBudget bounds one TypeScript check or example run.
const tsBudget = 5 * time.Minute

// syntaxCheck parses each file with the SDK's own TypeScript and prints
// {file: ["line: message"]} for the ones that do not parse. transpileModule
// reports syntax only, so a fragment that uses a `client` it never declares
// passes while a block with broken syntax does not.
const syntaxCheck = `import { createRequire } from "node:module";
import { readFileSync } from "node:fs";
const [sdkDir, ...files] = process.argv.slice(2);
const ts = createRequire(sdkDir + "/package.json")("typescript");
const out = {};
for (const f of files) {
  const r = ts.transpileModule(readFileSync(f, "utf8"), { reportDiagnostics: true, fileName: f,
    compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022, jsx: ts.JsxEmit.Preserve } });
  const msgs = (r.diagnostics || []).map((d) => (d.start !== undefined ?
    ts.getLineAndCharacterOfPosition(d.file, d.start).line + 1 : 0) + ": " + ts.flattenDiagnosticMessageText(d.messageText, " "));
  if (msgs.length) out[f] = msgs;
}
process.stdout.write(JSON.stringify(out));
`

// scriptLangs maps a fence language to the extension TypeScript parses it as.
var scriptLangs = map[string]string{"typescript": ".ts", "ts": ".ts", "tsx": ".tsx", "javascript": ".ts", "js": ".ts", "jsx": ".tsx"}

// requireNode skips when node, pnpm or the SDK's dependencies are missing.
func requireNode(t testing.TB) string {
	t.Helper()
	for _, tool := range []string{"node", "pnpm"} {
		if _, err := exec.LookPath(tool); err != nil {
			harness.SkipNotApplicable(t, tool+" is not installed on the runner: the TypeScript examples need Node 20+ and pnpm")
		}
	}
	sdk := filepath.Join(cliconf.RepoRoot(t), "sdk")
	if _, err := os.Stat(filepath.Join(sdk, "node_modules", "typescript")); err != nil {
		harness.SkipNotApplicable(t, "sdk/node_modules has no typescript: run `pnpm install --frozen-lockfile` in sdk/ before the run")
	}
	return sdk
}

func runTool(t testing.TB, dir string, env []string, tool string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), tsBudget)
	defer cancel()
	cmd := exec.CommandContext(ctx, tool, args...)
	cmd.Dir, cmd.Env = dir, env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestDocsExamples_scriptBlocksParse: every TypeScript and JavaScript block
// in the documents parses, so a reader pasting it gets no syntax error.
func TestDocsExamples_scriptBlocksParse(t *testing.T) {
	t.Parallel()
	sdk := requireNode(t)
	dir := t.TempDir()
	where := map[string]string{}
	args := []string{filepath.Join(dir, "check.mjs"), sdk}
	if err := os.WriteFile(args[0], []byte(syntaxCheck), 0o600); err != nil {
		t.Fatal(err)
	}
	langs := make([]string, 0, len(scriptLangs))
	for l := range scriptLangs {
		langs = append(langs, l)
	}
	for i, s := range allSnippets(t, langs...) {
		path := filepath.Join(dir, fmt.Sprintf("snippet-%d%s", i, scriptLangs[s.Lang]))
		if err := os.WriteFile(path, []byte(s.Body), 0o600); err != nil {
			t.Fatal(err)
		}
		where[path], args = s.Where(), append(args, path)
	}
	if len(where) == 0 {
		t.Fatal("no TypeScript or JavaScript block in the example documents: the extraction broke")
	}
	out, err := runTool(t, dir, toolEnv(), "node", args...)
	if err != nil {
		t.Fatalf("the syntax check did not run (%v):\n%s", err, out)
	}
	var bad map[string][]string
	if err := json.Unmarshal([]byte(out), &bad); err != nil {
		t.Fatalf("the syntax check printed no JSON (%v):\n%s", err, out)
	}
	for path, msgs := range bad {
		t.Errorf("%s: the block does not parse: %v", where[path], msgs)
	}
}

// sdkExamples are the runnable programs in sdk/examples.
func sdkExamples(t testing.TB, sdk string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(sdk, "examples", "*.ts"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no example under sdk/examples: %v", err)
	}
	return files
}

// TestDocsExamples_sdkExamplesTypecheck: the SDK's example programs
// type-check against the SDK source they import, strictly.
func TestDocsExamples_sdkExamplesTypecheck(t *testing.T) {
	t.Parallel()
	sdk := requireNode(t)
	args := append([]string{"--dir", sdk, "exec", "tsc", "--noEmit", "--strict", "--skipLibCheck", "--target", "es2022",
		"--module", "esnext", "--moduleResolution", "bundler", "--lib", "es2022,dom", "--types", "node"}, sdkExamples(t, sdk)...)
	if out, err := runTool(t, sdk, toolEnv(), "pnpm", args...); err != nil {
		t.Errorf("sdk/examples do not type-check (%v):\n%s", err, out)
	}
}
