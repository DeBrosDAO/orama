//go:build e2e_fleet

package cliconf

import (
	"regexp"
	"sort"
	"strings"
)

// Help is what cobra's default help template prints for one command.
type Help struct {
	// Usage are the lines under "Usage:", e.g. "orama app [flags]".
	Usage []string
	// Aliases are the command's other names (not its own name).
	Aliases []string
	// Subcommands are "Available Commands:" without cobra's help/completion.
	Subcommands []string
	// Shorts maps each subcommand to its one-line description.
	Shorts map[string]string
	// Flags are "Flags:" (the command's own, --help/-h excluded), as
	// "--name" and "-x" spellings, sorted.
	Flags []string
	// Global are "Global Flags:" (inherited), same form.
	Global []string
}

// Help section headers of cobra's default template.
const (
	secUsage    = "Usage:"
	secAliases  = "Aliases:"
	secExamples = "Examples:"
	secCommands = "Available Commands:"
	secFlags    = "Flags:"
	secGlobal   = "Global Flags:"
	secTopics   = "Additional help topics:"
)

var (
	sections = map[string]bool{secUsage: true, secAliases: true, secExamples: true,
		secCommands: true, secFlags: true, secGlobal: true, secTopics: true}
	// flagLineRe is one flag of pflag's FlagUsages: "  -f, --follow   ..." or "      --since string   ...".
	flagLineRe = regexp.MustCompile(`^\s+(?:-([A-Za-z0-9]), )?--([A-Za-z0-9][A-Za-z0-9-]*)(?:\s|$)`)
	// commandLineRe is one row of "Available Commands:".
	commandLineRe = regexp.MustCompile(`^  (\S+)\s+(.*)$`)
)

// cobraBuiltins are the commands cobra adds by itself; the reference omits them.
var cobraBuiltins = map[string]bool{"help": true, "completion": true}

// ParseHelp reads a `--help` output. Everything before "Usage:" is the
// command's long description and is not parsed, so a description that happens
// to contain a header word cannot confuse it.
func ParseHelp(out string) Help {
	h := Help{Shorts: map[string]string{}}
	section := ""
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimRight(line, " \r")
		if sections[trimmed] && (section != "" || trimmed == secUsage) {
			section = trimmed
			continue
		}
		if section == "" || strings.TrimSpace(trimmed) == "" {
			continue
		}
		h.add(section, trimmed)
	}
	sort.Strings(h.Flags)
	sort.Strings(h.Global)
	sort.Strings(h.Subcommands)
	sort.Strings(h.Aliases)
	return h
}

func (h *Help) add(section, line string) {
	switch section {
	case secUsage:
		h.Usage = append(h.Usage, strings.TrimSpace(line))
	case secAliases:
		names := strings.Split(strings.TrimSpace(line), ", ")
		h.Aliases = append(h.Aliases, names[1:]...)
	case secCommands:
		if m := commandLineRe.FindStringSubmatch(line); m != nil && !cobraBuiltins[m[1]] {
			h.Subcommands = append(h.Subcommands, m[1])
			h.Shorts[m[1]] = strings.TrimSpace(m[2])
		}
	case secFlags:
		h.Flags = append(h.Flags, flagSpellings(line, true)...)
	case secGlobal:
		h.Global = append(h.Global, flagSpellings(line, false)...)
	}
}

// flagSpellings returns "--name" (and "-x") of a flag line; dropHelp leaves
// out cobra's own --help, which the reference does not list.
func flagSpellings(line string, dropHelp bool) []string {
	m := flagLineRe.FindStringSubmatch(line)
	if m == nil || (dropHelp && m[2] == "help") {
		return nil
	}
	out := []string{"--" + m[2]}
	if m[1] != "" {
		out = append(out, "-"+m[1])
	}
	return out
}

// HasGlobal reports whether flag (e.g. "--json") is inherited.
func (h Help) HasGlobal(flag string) bool {
	for _, f := range h.Global {
		if f == flag {
			return true
		}
	}
	return false
}
