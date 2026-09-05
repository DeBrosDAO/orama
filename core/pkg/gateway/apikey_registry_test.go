package gateway

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"net"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/logging"
)

func testLoggerForRegistry(t *testing.T) *logging.ColoredLogger {
	t.Helper()
	l, err := logging.NewColoredLogger(logging.ComponentGeneral, false)
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	return l
}

// The index gateway's own database is the registry, so there is nothing
// separate to connect to and nothing to fail.
func TestConnectAPIKeyRegistry_noSeparateRegistryConfigured(t *testing.T) {
	for _, cfg := range []*Config{
		{RQLiteDSN: "http://localhost:10100"},
		{RQLiteDSN: "http://localhost:10100", GlobalRQLiteDSN: "http://localhost:10100"},
	} {
		client, err := connectAPIKeyRegistry(cfg, testLoggerForRegistry(t))
		if err != nil {
			t.Errorf("connectAPIKeyRegistry: %v", err)
		}
		if client != nil {
			t.Error("a client was created for a gateway whose own database is the registry")
		}
	}
}

func TestUsesSeparateAPIKeyRegistry(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  *Config
		want bool
	}{
		{"namespace gateway", &Config{RQLiteDSN: "http://10.0.0.5:15000", GlobalRQLiteDSN: "http://10.0.0.5:10100"}, true},
		{"index gateway", &Config{RQLiteDSN: "http://localhost:10100"}, false},
		{"same dsn", &Config{RQLiteDSN: "http://x:1", GlobalRQLiteDSN: "http://x:1"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := usesSeparateAPIKeyRegistry(tc.cfg); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// The fail-open this closes. A gateway that could not reach the registry used
// to carry on, and apiKeyDB() then fell back to the local database — the
// tenant's own rqlite on a namespace gateway, whose api_keys table the tenant
// can write. It does not start now.
func TestConnectAPIKeyRegistry_anUnreachableRegistryIsFatal(t *testing.T) {
	// A port nothing is listening on: bind one, read its number, close it.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := listener.Addr().String()
	listener.Close()

	cfg := &Config{
		RQLiteDSN:       "http://127.0.0.1:1",
		GlobalRQLiteDSN: "http://" + addr,
	}
	client, err := connectAPIKeyRegistry(cfg, testLoggerForRegistry(t))
	if err == nil {
		t.Fatal("a gateway configured with an unreachable API-key registry was allowed to continue")
	}
	if client != nil {
		t.Error("a client was returned alongside the error")
	}
	if !strings.Contains(err.Error(), addr) {
		t.Errorf("the error does not name the registry it could not reach: %v", err)
	}
	if !strings.Contains(err.Error(), "no safe store") {
		t.Errorf("the error does not say why this is fatal rather than degraded: %v", err)
	}
}

// A credential in a URL reaches the access log, the Referer of whatever the
// page loads next, and the browser's history. There were two copies of the
// extraction and they disagreed about this; a third would disagree again.
// Reading api_key or token out of a query string belongs in exactly one place.
func TestQueryStringCredentials_areReadInOnePlaceOnly(t *testing.T) {
	dirs := map[string]string{
		".":                     "gateway",
		"./auth":                "auth",
		"./handlers/auth":       "handlers/auth",
		"./handlers/serverless": "handlers/serverless",
		"./handlers/storage":    "handlers/storage",
		"./handlers/namespace":  "handlers/namespace",
	}
	allowed := map[string]bool{
		// The shared extractor, which takes the decision as a parameter.
		"auth/apikey_request.go": true,
	}

	found := map[string]bool{}
	for dir, label := range dirs {
		fset := token.NewFileSet()
		pkgs, err := parser.ParseDir(fset, dir, func(fi fs.FileInfo) bool {
			return !strings.HasSuffix(fi.Name(), "_test.go")
		}, parser.ParseComments)
		if err != nil {
			continue // a directory that does not exist is not a finding
		}
		for _, pkg := range pkgs {
			for path, file := range pkg.Files {
				// A read whose result is compared straight to "" asks
				// whether a parameter is present; it does not take the
				// credential out. The diagnostics on the WebSocket reject
				// path do exactly that, and they are not what this guards.
				presence := map[ast.Node]bool{}
				ast.Inspect(file, func(n ast.Node) bool {
					bin, ok := n.(*ast.BinaryExpr)
					if !ok || (bin.Op != token.NEQ && bin.Op != token.EQL) {
						return true
					}
					for _, side := range []ast.Expr{bin.X, bin.Y} {
						if lit, ok := side.(*ast.BasicLit); ok && lit.Kind == token.STRING && lit.Value == `""` {
							presence[bin.X] = true
							presence[bin.Y] = true
						}
					}
					return true
				})

				ast.Inspect(file, func(n ast.Node) bool {
					if presence[n] || !readsCredentialFromQueryString(n) {
						return true
					}
					found[label+"/"+path[strings.LastIndexByte(path, '/')+1:]] = true
					return true
				})
			}
		}
	}

	if len(found) == 0 {
		t.Fatal("no query-string credential read was found anywhere; this test is not looking where it thinks it is")
	}
	for name := range found {
		if !allowed[name] {
			t.Errorf("%s reads a credential out of a query string. That belongs in "+
				"auth.APIKeyFromRequest, which takes the decision as a parameter — two copies of "+
				"it drifted once already, and one of them accepted a key in the URL of an "+
				"ordinary POST.", name)
		}
	}
}

// readsCredentialFromQueryString matches `<something>.Query().Get("api_key")`
// and the same for "token" — a read of a credential out of the URL, as opposed
// to a string that merely happens to spell one of those words.
func readsCredentialFromQueryString(n ast.Node) bool {
	call, ok := n.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Get" {
		return false
	}
	inner, ok := sel.X.(*ast.CallExpr)
	if !ok {
		return false
	}
	innerSel, ok := inner.Fun.(*ast.SelectorExpr)
	if !ok || innerSel.Sel.Name != "Query" {
		return false
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return false
	}
	return lit.Value == `"api_key"` || lit.Value == `"token"`
}
