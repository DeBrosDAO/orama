// Package coverage is the gate that makes every shipped surface either tested
// by a feature package or waived with a reason: CLI commands, gateway routes,
// chain Msg and Query RPCs, and systemd unit templates. It reads the same
// generated documents and sources the product is checked against, so a new
// command, route or message fails `make test` until its e2e test exists.
package coverage

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/DeBrosOfficial/network/e2e/harness/manifest"
)

// Sources of the universe, relative to the repository root.
const (
	CLIReferencePath = "docs/whitepaper/technical-reference/appendices/d-cli-reference.md"
	APISurfacePath   = "docs/whitepaper/technical-reference/appendices/i-api-surface.md"
	ProtoDir         = "chain/proto"
	SystemdDir       = "core/systemd"
)

// Kinds of universe items.
const (
	KindCLI   = "cli"
	KindRoute = "route"
	KindMsg   = "msg"
	KindQuery = "query"
	KindUnit  = "unit"
)

// Item is one shipped thing that must be covered or waived.
type Item struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Source string `json:"source"`
	// Note is extra context from the source, such as a route's owner.
	Note string `json:"note,omitempty"`
}

var (
	cliHeading = regexp.MustCompile(`^## (orama(?: [a-z0-9][a-z0-9-]*)*)\s*$`)
	routeRow   = regexp.MustCompile("^\\| `(/[^`]*)` \\| ([A-Za-z]+) \\|")
	protoPkg   = regexp.MustCompile(`^package\s+([a-z0-9_.]+)\s*;`)
	protoSvc   = regexp.MustCompile(`^service\s+(Msg|Query)\s*\{`)
	protoRPC   = regexp.MustCompile(`^rpc\s+([A-Za-z0-9_]+)\s*\(\s*(?:stream\s+)?([A-Za-z0-9_.]+)\s*\)`)
)

// Universe enumerates every item under repoRoot, sorted by id. A missing
// source is an error: an empty universe would pass the gate by covering nothing.
func Universe(repoRoot string) ([]Item, error) {
	var all []Item
	for _, enum := range []func(string) ([]Item, error){CLICommands, Routes, ChainRPCs, Units} {
		items, err := enum(repoRoot)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	return all, nil
}

// CLICommands reads every "## orama ..." heading of the generated CLI reference (whitepaper appendix D).
// Group commands are included: `orama app` prints its subcommands, and that
// output is part of what ships.
func CLICommands(repoRoot string) ([]Item, error) {
	return scanLines(repoRoot, CLIReferencePath, func(line string) *Item {
		m := cliHeading.FindStringSubmatch(line)
		if m == nil {
			return nil
		}
		return &Item{ID: manifest.PrefixCLI + m[1], Kind: KindCLI, Source: CLIReferencePath}
	})
}

// Routes reads every route row of the API surface tables. The document has no
// method column, so ids are paths; the owner is kept as the note.
func Routes(repoRoot string) ([]Item, error) {
	return scanLines(repoRoot, APISurfacePath, func(line string) *Item {
		m := routeRow.FindStringSubmatch(line)
		if m == nil {
			return nil
		}
		return &Item{ID: manifest.PrefixRoute + m[1], Kind: KindRoute, Source: APISurfacePath, Note: m[2]}
	})
}

func scanLines(repoRoot, rel string, match func(string) *Item) ([]Item, error) {
	f, err := os.Open(filepath.Join(repoRoot, rel))
	if err != nil {
		return nil, fmt.Errorf("failed to open coverage source %s: %w", rel, err)
	}
	defer f.Close()
	seen := map[string]bool{}
	var out []Item
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if it := match(sc.Text()); it != nil && !seen[it.ID] {
			seen[it.ID] = true
			out = append(out, *it)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("failed to read coverage source %s: %w", rel, err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("coverage source %s yielded no items: its format changed", rel)
	}
	return out, nil
}

// ChainRPCs reads the Msg service of every tx.proto (msg:<package>.<request
// type>, the type URL a transaction carries) and the Query service of every
// query.proto (query:<package>.<rpc>).
func ChainRPCs(repoRoot string) ([]Item, error) {
	root := filepath.Join(repoRoot, ProtoDir)
	var out []Item
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || (d.Name() != "tx.proto" && d.Name() != "query.proto") {
			return nil
		}
		items, perr := parseProto(repoRoot, path)
		out = append(out, items...)
		return perr
	})
	if err != nil {
		return nil, fmt.Errorf("failed to enumerate chain protos under %s: %w", ProtoDir, err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no Msg or Query RPCs found under %s", ProtoDir)
	}
	return out, nil
}

func parseProto(repoRoot, path string) ([]Item, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", path, err)
	}
	rel, err := filepath.Rel(repoRoot, path)
	if err != nil {
		return nil, fmt.Errorf("failed to relate %s to the repository root: %w", path, err)
	}
	var pkg, service string
	// inRPC is true inside an rpc's own braces (`rpc X(...) returns (...) {` holding an option),
	// whose closing brace is not the end of the service.
	inRPC := false
	var out []Item
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if m := protoPkg.FindStringSubmatch(line); m != nil {
			pkg = m[1]
		}
		if m := protoSvc.FindStringSubmatch(line); m != nil {
			service = m[1]
			continue
		}
		if line == "}" {
			if inRPC {
				inRPC = false
				continue
			}
			service = ""
		}
		m := protoRPC.FindStringSubmatch(line)
		if m == nil || service == "" {
			continue
		}
		inRPC = strings.HasSuffix(line, "{")
		if pkg == "" {
			return nil, fmt.Errorf("%s declares rpc %s before its package", rel, m[1])
		}
		out = append(out, rpcItem(pkg, service, m[1], m[2], rel))
	}
	return out, nil
}

func rpcItem(pkg, service, rpc, reqType, source string) Item {
	if service == "Msg" {
		short := reqType[strings.LastIndex(reqType, ".")+1:]
		return Item{ID: manifest.PrefixMsg + pkg + "." + short, Kind: KindMsg, Source: source}
	}
	return Item{ID: manifest.PrefixQuery + pkg + "." + rpc, Kind: KindQuery, Source: source}
}

// Units lists the systemd unit templates install ships.
func Units(repoRoot string) ([]Item, error) {
	dir := filepath.Join(repoRoot, SystemdDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to list unit templates in %s: %w", SystemdDir, err)
	}
	var out []Item
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !(strings.HasSuffix(name, ".service") || strings.HasSuffix(name, ".timer")) {
			continue
		}
		out = append(out, Item{ID: manifest.PrefixUnit + name, Kind: KindUnit, Source: SystemdDir + "/" + name})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no unit templates in %s", SystemdDir)
	}
	return out, nil
}
