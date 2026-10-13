package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/gateway"
	"github.com/DeBrosOfficial/network/pkg/gateway/routepolicy"
)

// routeSources are the files whose mux.Handle and mux.HandleFunc calls name a
// handler for a literal pattern.
var routeSources = []string{
	"core/pkg/gateway/routes.go",
	"core/pkg/gateway/handlers/serverless/routes.go",
}

// ormHandler names the handler of the routes the rqlite ORM gateway composes
// from its base path, which no literal pattern in routeSources names.
const ormHandler = "rqlite.HTTPGateway (core/pkg/rqlite/gateway.go)"

// dynamicNotes says how each dynamic route's policy depends on the request.
// A dynamic route without a note fails the generator, so a new one cannot be
// published unexplained.
var dynamicNotes = map[string]string{
	"/v1/functions/":     "by operation: invoke is open (narrowed by a fn grant), a WebSocket opened with a capability is handler-auth, a WebSocket otherwise needs fn:invoke, everything else fn:manage with a namespace grant",
	"/v1/network/status": "a request carrying a coordination MAC is handler-auth; any other needs operator:read and the operator list, on the main gateway",
	"/v1/network/peers":  "a request carrying a coordination MAC is handler-auth; any other needs operator:read and the operator list, on the main gateway",
	"/v1/storage/unpin/": "DELETE needs storage:write with any token (a key exchanged for a token is enough); any other method needs storage:write with a principal token",
}

// routeRow is one line of Appendix C.
type routeRow struct {
	pattern string
	handler string
	policy  routepolicy.Policy
	dynamic bool
}

// genRoutes renders Appendix C from the gateway's route policy table, so a
// route cannot be served without appearing here.
func genRoutes(b *Book) ([]byte, error) {
	handlers, err := routeHandlers(b)
	if err != nil {
		return nil, err
	}
	return renderRoutes(gateway.RoutePolicies(), handlers, titleOf(b, "appendices/c-gateway-routes.md"))
}

// renderRoutes renders the table. handlers maps a pattern to the handler that
// serves it.
func renderRoutes(table *routepolicy.Table, handlers map[string]string, title string) ([]byte, error) {
	groups := map[string][]routeRow{}
	var names []string
	for _, pattern := range table.Patterns() {
		row := routeRow{pattern: pattern, handler: handlers[pattern]}
		if p, ok := table.Static(pattern); ok {
			row.policy = p
		} else {
			row.dynamic = true
			if dynamicNotes[pattern] == "" {
				return nil, fmt.Errorf("route %s has a request-dependent policy but gen_routes.go:dynamicNotes does not describe it", pattern)
			}
		}
		g := routeGroup(pattern)
		if _, seen := groups[g]; !seen {
			names = append(names, g)
		}
		groups[g] = append(groups[g], row)
	}
	sort.Strings(names)

	var sb strings.Builder
	sb.WriteString(generatedHeader(title, "the gateway's route policy table (`core/pkg/gateway/route_policy.go:buildRoutePolicies`) and the handlers `routes.go` mounts"))
	sb.WriteString(routesIntro)
	for _, g := range names {
		fmt.Fprintf(&sb, "\n## %s\n\n| Route | Handler | Access | Grant | Token | Notes |\n|---|---|---|---|---|---|\n", g)
		for _, r := range groups[g] {
			sb.WriteString(routeLine(r))
		}
	}
	sb.WriteString("\n## Routes whose policy depends on the request\n\n| Route | Policy |\n|---|---|\n")
	var dyn []string
	for p := range dynamicNotes {
		if table.Declared(p) {
			dyn = append(dyn, p)
		}
	}
	sort.Strings(dyn)
	for _, p := range dyn {
		fmt.Fprintf(&sb, "| `%s` | %s |\n", p, tableCell(dynamicNotes[p]))
	}
	return []byte(sb.String()), nil
}

const routesIntro = `Every route the gateway serves, with the policy the route table declares for it: what credential the middleware insists on, which grant a caller needs, and which token. A pattern ending in a slash matches every path beneath it. A route absent from the policy table cannot be registered, so this list is complete. [Gateway architecture](../vol1/12-gateway-architecture.md) explains the middleware and [Authorization](../vol1/14-authorization.md) explains grants.

Access is ` + "`credential`" + ` (an API key or a JWT, resolved by the middleware), ` + "`open`" + ` (anyone) or ` + "`handler-auth`" + ` (the handler authenticates the caller itself: an invite token, a cluster-secret or coordination MAC). Grant is ` + "`domain:action`" + `; an empty grant means any valid credential. Token is the token kind required on top of the grant: ` + "`any`" + ` (a bare key), ` + "`token`" + ` (a JWT of any kind), ` + "`wallet`" + ` (a logged-in user) or ` + "`principal`" + ` (a user or a deployed app's workload token). Notes lists: ` + "`owned`" + ` (a live grant in the namespace), ` + "`main`" + ` (always served by the index gateway, never proxied to a namespace gateway), ` + "`narrowed`" + ` (an open route that still applies a grant's resource selector), ` + "`no-address`" + ` and ` + "`no-log`" + ` (what the request log keeps).
`

// routeGroup is the section a route belongs to: its first two path segments.
func routeGroup(pattern string) string {
	parts := strings.Split(strings.Trim(pattern, "/"), "/")
	switch {
	case parts[0] == "v1" && len(parts) > 1:
		return "/v1/" + parts[1]
	case parts[0] == "":
		return "/"
	default:
		return "/" + parts[0]
	}
}

func routeLine(r routeRow) string {
	handler := "-"
	if r.handler != "" {
		handler = "`" + r.handler + "`"
	}
	if r.dynamic {
		return fmt.Sprintf("| `%s` | %s | by request | | | see below |\n", r.pattern, handler)
	}
	p := r.policy
	grant := ""
	if p.Domain != "" || p.Action != "" {
		grant = "`" + p.Domain + ":" + p.Action + "`"
	}
	return fmt.Sprintf("| `%s` | %s | %s | %s | %s | %s |\n", r.pattern, handler, p.Access, grant, tokenName(p.Token), routeNotes(p))
}

func tokenName(t routepolicy.TokenRequirement) string {
	switch t {
	case routepolicy.AnyToken:
		return "token"
	case routepolicy.WalletToken:
		return "wallet"
	case routepolicy.PrincipalToken:
		return "principal"
	default:
		return "any"
	}
}

func routeNotes(p routepolicy.Policy) string {
	var notes []string
	if p.Ownership {
		notes = append(notes, "owned")
	}
	if p.MainGateway {
		notes = append(notes, "main")
	}
	if p.NarrowedByGrant {
		notes = append(notes, "narrowed")
	}
	switch p.RequestLog {
	case routepolicy.LogNoAddress:
		notes = append(notes, "no-address")
	case routepolicy.LogNone:
		notes = append(notes, "no-log")
	}
	return strings.Join(notes, ", ")
}

// routeHandlers maps every literal pattern in routeSources to the handler
// expression registered for it, and the ORM gateway's patterns to ormHandler.
func routeHandlers(b *Book) (map[string]string, error) {
	out := map[string]string{}
	for _, rel := range routeSources {
		found, err := muxHandlers(filepath.Join(b.Root, filepath.FromSlash(rel)))
		if err != nil {
			return nil, err
		}
		for pattern, handler := range found {
			out[pattern] = handler
		}
	}
	for _, pattern := range gateway.RoutePolicies().Patterns() {
		if strings.HasPrefix(pattern, "/v1/rqlite/") && out[pattern] == "" {
			out[pattern] = ormHandler
		}
	}
	return out, nil
}

// muxHandlers returns pattern to handler text for every Handle or HandleFunc
// call in a file whose first argument is a string literal.
func muxHandlers(path string) (map[string]string, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", path, err)
	}
	return muxHandlersFromSource(path, src)
}

func muxHandlersFromSource(name string, src []byte) (map[string]string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, src, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", name, err)
	}
	out := map[string]string{}
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) < 2 {
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
		pattern, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		if _, dup := out[pattern]; !dup {
			out[pattern] = exprText(src, fset, call.Args[1])
		}
		return true
	})
	return out, nil
}

// exprText is the source text of an expression, flattened to one line and
// shortened to the handler it names: g.withHomeNodeOnly(g.x.Y) becomes g.x.Y,
// and a function literal becomes "inline handler".
func exprText(src []byte, fset *token.FileSet, e ast.Expr) string {
	switch v := e.(type) {
	case *ast.FuncLit:
		return "inline handler"
	case *ast.CallExpr:
		if len(v.Args) == 1 {
			return exprText(src, fset, v.Args[0])
		}
	}
	return strings.Join(strings.Fields(string(src[fset.Position(e.Pos()).Offset:fset.Position(e.End()).Offset])), " ")
}
