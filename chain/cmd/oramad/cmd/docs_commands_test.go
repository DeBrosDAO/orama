package cmd

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// docsDir is the repository's documentation, from this package.
const docsDir = "../../../../docs"

// txCommandMention finds `oramad tx <group> [<verb>]` in prose. Whitespace
// between the words may be a line break.
var txCommandMention = regexp.MustCompile("oramad tx\\s+([a-z][a-z-]*)(?:\\s+([a-z][a-z-]*))?")

func subcommand(parent *cobra.Command, name string) *cobra.Command {
	for _, c := range parent.Commands() {
		if c.Name() == name || c.HasAlias(name) {
			return c
		}
	}
	return nil
}

// docs/whitepaper/technical-reference/vol2/39-chain-architecture.md names `oramad tx houses submit-proposal`, and
// x/houses has no tx command of its own (client/cli/query.go only). The
// command exists all the same: AutoCLI generates `oramad tx houses` from the
// module's Msg service. Every `oramad tx` command a document names is checked
// against the command tree the binary builds, wherever it comes from.
func TestDocs_oramadTxCommandsExist(t *testing.T) {
	tx := subcommand(NewRootCmd(), "tx")
	if tx == nil {
		t.Fatal("oramad has no tx command")
	}
	err := filepath.WalkDir(docsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range txCommandMention.FindAllStringSubmatch(string(body), -1) {
			group := subcommand(tx, m[1])
			if group == nil {
				t.Errorf("%s names `oramad tx %s`, which oramad does not have", path, m[1])
				continue
			}
			// A word after a command with no subcommands is prose.
			if m[2] != "" && group.HasSubCommands() && subcommand(group, m[2]) == nil {
				t.Errorf("%s names `oramad tx %s %s`, which oramad does not have", path, m[1], m[2])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSubcommand_findsByNameAndAlias(t *testing.T) {
	root := &cobra.Command{Use: "root"}
	root.AddCommand(&cobra.Command{Use: "query", Aliases: []string{"q"}})
	if subcommand(root, "query") == nil || subcommand(root, "q") == nil {
		t.Error("a command is found by its name and by its alias")
	}
	if subcommand(root, "tx") != nil {
		t.Error("a command that is not there was found")
	}
}
