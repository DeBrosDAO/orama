package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/install"
	"github.com/spf13/cobra"
)

// The run-your-own-cluster page is the installer's checks and the commands
// that exist. A renamed flag or a changed floor fails here.
func TestRunYourOwnClusterGuideMatchesTheInstaller(t *testing.T) {
	path := filepath.Join(repoRoot(t), "website", "src", "docs", "operator", "run-your-own-cluster.mdx")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)

	for _, want := range []string{
		install.SupportedReleasesText(),
		strconv.Itoa(install.MinCPUCores) + " CPU",
		strconv.Itoa(install.MinRAMBytes/(1024*1024*1024)) + "GB",
		strconv.Itoa(install.MinFreeDiskBytes/(1024*1024*1024)) + "GB",
		"22/tcp",
		"51820/udp",
		"80/tcp",
		"443/tcp",
		"53/tcp",
		"53/udp",
		"letsencrypt-staging",
		"registry TLD",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("guide is missing %q", want)
		}
	}

	root := newRootCmd()
	var commands int
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "orama ") {
			continue
		}
		commands++
		if err := findGuideCommand(root, line); err != nil {
			t.Errorf("%s: %v", line, err)
		}
	}
	if commands < 8 {
		t.Fatalf("guide has %d orama commands, want at least the setup, delegation, login and deploy sequence", commands)
	}
}

func findGuideCommand(root *cobra.Command, line string) error {
	fields := strings.Fields(strings.TrimPrefix(line, "orama "))
	cmd := root
	for _, field := range fields {
		if strings.HasPrefix(field, "-") {
			break
		}
		next := findSubcommand(cmd, field)
		if next == nil {
			if cmd == root {
				return errUnknownCommand(cmd, field)
			}
			break
		}
		cmd = next
	}
	if cmd == root {
		return errUnknownCommand(root, fields[0])
	}
	return nil
}

func findSubcommand(cmd *cobra.Command, name string) *cobra.Command {
	for _, c := range cmd.Commands() {
		if c.Name() == name {
			return c
		}
	}
	return nil
}

type unknownCommand struct {
	parent string
	name   string
}

func errUnknownCommand(parent *cobra.Command, name string) error {
	return unknownCommand{parent: parent.Name(), name: name}
}

func (e unknownCommand) Error() string {
	return e.parent + " has no subcommand " + e.name
}
