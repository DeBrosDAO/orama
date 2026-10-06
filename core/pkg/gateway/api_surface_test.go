package gateway

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// The gateway's HTTP surface is the contract every client is written against,
// and it had grown to more than a hundred routes with no record of which client
// owns which. The TypeScript SDK covered roughly a third of it and nobody could
// say whether the rest was a gap or a decision.
//
// docs/API_SURFACE.md is that record. This test keeps it honest in both
// directions: a route registered in the gateway and missing from the document
// fails, and a route documented but no longer registered fails too.

// routePattern matches the first path segment of a registered pattern, which is
// all a ServeMux pattern is here — no host, no method prefix.
var routePattern = regexp.MustCompile(`^/`)

// registeredRoutes walks the sources that register handlers and returns every
// pattern they mount.
func registeredRoutes(t *testing.T) []string {
	t.Helper()

	repoRoot := repoRootFor(t)
	seen := map[string]struct{}{}

	// Files whose mux.Handle / mux.HandleFunc calls take a literal pattern.
	for _, rel := range []string{
		"core/pkg/gateway/routes.go",
		"core/pkg/gateway/handlers/serverless/routes.go",
	} {
		for _, pattern := range literalMuxPatterns(t, filepath.Join(repoRoot, rel)) {
			seen[pattern] = struct{}{}
		}
	}

	// The rqlite ORM gateway composes its patterns from a base path, so it
	// reports them itself rather than being guessed at from the source.
	orm := &rqlite.HTTPGateway{BasePath: "/v1/rqlite"}
	for _, pattern := range orm.Routes() {
		seen[pattern] = struct{}{}
	}

	out := make([]string, 0, len(seen))
	for pattern := range seen {
		out = append(out, pattern)
	}
	sort.Strings(out)
	return out
}

// literalMuxPatterns returns the string literal passed as the first argument to
// every mux.Handle / mux.HandleFunc call in a file.
func literalMuxPatterns(t *testing.T, path string) []string {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	var patterns []string
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || (sel.Sel.Name != "HandleFunc" && sel.Sel.Name != "Handle") {
			return true
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(lit.Value)
		if err != nil || !routePattern.MatchString(value) || value == "/" {
			return true
		}
		patterns = append(patterns, value)
		return true
	})
	return patterns
}

// documentedRoutes returns every route named in the first column of a table row
// in docs/API_SURFACE.md.
func documentedRoutes(t *testing.T) map[string]string {
	t.Helper()

	body, err := os.ReadFile(filepath.Join(repoRootFor(t), "docs/API_SURFACE.md"))
	if err != nil {
		t.Fatalf("read API_SURFACE.md: %v", err)
	}

	// Only rows whose first cell is a route: the legend at the top of the
	// document is a table too.
	row := regexp.MustCompile("^\\|\\s*`(/[^`]*)`\\s*\\|\\s*([^|]+?)\\s*\\|")
	out := map[string]string{}
	for _, line := range strings.Split(string(body), "\n") {
		m := row.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		out[m[1]] = strings.TrimSpace(m[2])
	}
	return out
}

func repoRootFor(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// .../core/pkg/gateway -> repo root
	return filepath.Clean(filepath.Join(wd, "..", "..", ".."))
}

func TestEveryRegisteredRouteIsDocumented(t *testing.T) {
	documented := documentedRoutes(t)
	routes := registeredRoutes(t)

	if len(routes) < 100 {
		t.Fatalf("found only %d routes — the collector is broken, not the gateway", len(routes))
	}

	var missing []string
	for _, route := range routes {
		if _, ok := documented[route]; !ok {
			missing = append(missing, route)
		}
	}
	if len(missing) > 0 {
		t.Errorf("routes registered but absent from docs/API_SURFACE.md — decide who owns each one:\n  %s",
			strings.Join(missing, "\n  "))
	}
}

func TestEveryDocumentedRouteExists(t *testing.T) {
	documented := documentedRoutes(t)
	registered := map[string]struct{}{}
	for _, route := range registeredRoutes(t) {
		registered[route] = struct{}{}
	}

	var stale []string
	for route := range documented {
		if _, ok := registered[route]; !ok {
			stale = append(stale, route)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf("documented in docs/API_SURFACE.md but no longer registered:\n  %s",
			strings.Join(stale, "\n  "))
	}
}

// Every route carries an owner, so the SDK's coverage is a decision rather than
// an accident.
func TestEveryDocumentedRouteHasAnOwner(t *testing.T) {
	valid := map[string]struct{}{
		"SDK":      {}, // @debros/orama calls it
		"CLI":      {}, // the orama CLI calls it; an application has no reason to
		"internal": {}, // node-to-node over WireGuard, never a client
		"direct":   {}, // reachable by a client, but not through the SDK by design
	}

	for route, owner := range documentedRoutes(t) {
		if _, ok := valid[owner]; !ok {
			t.Errorf("route %s has owner %q, which is not one of SDK, CLI, internal, direct", route, owner)
		}
	}
}
