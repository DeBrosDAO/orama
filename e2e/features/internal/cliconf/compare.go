//go:build e2e_fleet

package cliconf

import (
	"fmt"
	"slices"
	"strings"
)

// flagsMarker is what the reference and cobra append to a usage line when the
// command has flags.
const flagsMarker = " [flags]"

// Diff lists every way the live help of c disagrees with the reference.
// Empty means the documentation and the binary agree.
func Diff(ref *Reference, c Command, h Help) []string {
	var out []string
	if !slices.Equal(c.Flags, h.Flags) {
		out = append(out, fmt.Sprintf("flags: reference %v, binary %v", c.Flags, h.Flags))
	}
	if subs := ref.Listed(c); !slices.Equal(subs, h.Subcommands) {
		out = append(out, fmt.Sprintf("subcommands: reference %v, binary %v", subs, h.Subcommands))
	}
	if !slices.Equal(c.Aliases, h.Aliases) {
		out = append(out, fmt.Sprintf("aliases: reference %v, binary %v", c.Aliases, h.Aliases))
	}
	if !usageMatches(c.Usage, h.Usage) {
		out = append(out, fmt.Sprintf("usage: reference %q, binary %q", c.Usage, h.Usage))
	}
	if !h.HasGlobal("--" + JSONFlag) {
		out = append(out, "--json is not an inherited (global) flag")
	}
	return out
}

// deprecatedPrefix starts the one-line description of a deprecated command.
// Cobra leaves a deprecated command out of its parent's "Available Commands"
// while the reference, generated from the same tree, still documents it.
const deprecatedPrefix = "Deprecated"

// Listed is c's subcommands as its help lists them: the documented ones
// without the deprecated ones.
func (r *Reference) Listed(c Command) []string {
	var out []string
	for _, sub := range c.Subcommands {
		if child, ok := r.Get(c.Path + " " + sub); ok && strings.HasPrefix(child.Short, deprecatedPrefix) {
			continue
		}
		out = append(out, sub)
	}
	return out
}

// usageMatches: the reference usage without [flags] starts one of cobra's usage lines.
func usageMatches(ref string, live []string) bool {
	want := strings.TrimSuffix(ref, flagsMarker)
	for _, l := range live {
		if l == want || strings.HasPrefix(l, want+" ") {
			return true
		}
	}
	return false
}

// Positional describes a command's positional arguments as its usage line
// declares them.
type Positional struct {
	// Required is the number of <arg> placeholders.
	Required int
	// Open is true when more may follow: an optional [arg], "..." or "[-- command]".
	Open bool
}

// PositionalOf reads the placeholders of c's usage line.
func PositionalOf(c Command) Positional {
	rest := strings.TrimPrefix(strings.TrimSuffix(c.Usage, flagsMarker), c.Path)
	var p Positional
	for _, tok := range strings.Fields(rest) {
		switch {
		case strings.HasSuffix(tok, "..."):
			p.Open = true
			p.Required++
		case strings.HasPrefix(tok, "<"):
			p.Required++
		case strings.HasPrefix(tok, "["):
			p.Open = true
		}
	}
	return p
}
