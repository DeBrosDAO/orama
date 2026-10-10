//go:build e2e_fleet

package cliconf

import (
	"reflect"
	"testing"
)

const sampleReference = "# CLI reference\n\n## Commands\n\n- [`orama app`](#orama-app) - Manage\n\n" +
	"## orama app\n\nManage &lt;apps> &#123;x&#125;\n\n```text\norama app\n```\n\n" +
	"Aliases: `a`\n\n```text\nLong help.\n## orama not-a-command\n| Flag | Default | Description |\n```\n\n" +
	"| Flag | Default | Description |\n|---|---|---|\n| `-n`, `--lines` | `10` | how many |\n| `--json` | — | json |\n\n" +
	"Subcommands: `get`, `list`\n\n" +
	"## orama app get\n\nGet one\n\n```text\norama app get <name> [flags]\n```\n\n"

func TestParseReference_bookFormat(t *testing.T) {
	ref, err := ParseReference(sampleReference)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ref.Paths(), []string{"orama app", "orama app get"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("paths = %v, want %v (a heading inside a fence is not a command)", got, want)
	}
	app, _ := ref.Get("orama app")
	if app.Short != "Manage <apps> {x}" {
		t.Errorf("short = %q", app.Short)
	}
	if app.Usage != "orama app" || !reflect.DeepEqual(app.Aliases, []string{"a"}) {
		t.Errorf("usage %q aliases %v", app.Usage, app.Aliases)
	}
	if want := []string{"--json", "--lines", "-n"}; !reflect.DeepEqual(app.Flags, want) {
		t.Errorf("flags = %v, want %v (the fenced long help has a flag header of its own)", app.Flags, want)
	}
	if want := []string{"get", "list"}; !reflect.DeepEqual(app.Subcommands, want) {
		t.Errorf("subcommands = %v", app.Subcommands)
	}
	get, _ := ref.Get("orama app get")
	if get.Usage != "orama app get <name> [flags]" {
		t.Errorf("usage = %q", get.Usage)
	}
}

func TestParseReference_noCommands(t *testing.T) {
	if _, err := ParseReference("# CLI reference\n\n## Commands\n"); err == nil {
		t.Fatal("a reference with no command sections was accepted")
	}
}

func TestParseReference_realAppendix(t *testing.T) {
	ref := LoadReference(t)
	if len(ref.Commands) < 100 {
		t.Fatalf("parsed %d commands from %s, want at least 100", len(ref.Commands), ReferencePath)
	}
	for _, c := range ref.Commands {
		if c.Usage == "" {
			t.Errorf("%s has no usage line", c.Path)
		}
	}
}
