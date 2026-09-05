package auth

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	authsvc "github.com/DeBrosOfficial/network/pkg/gateway/auth"
)

// A login against a namespace another wallet owns is the caller's answer, not a
// server fault. It used to be neither: the ownership row was written without
// looking, so the request succeeded and returned an admin key for someone
// else's namespace.
func TestWriteCredentialError_notOwnedIsA403WithACode(t *testing.T) {
	rec := httptest.NewRecorder()

	writeCredentialError(rec, "anchat", &authsvc.ErrNamespaceOwnedByAnother{Namespace: "anchat"})

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want %d", rec.Code, http.StatusForbidden)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["code"] != ErrCodeNamespaceNotOwned {
		t.Errorf("code %v, want %s — a client that has to read the prose to tell "+
			"this from a broken gateway cannot act on it", body["code"], ErrCodeNamespaceNotOwned)
	}
	if body["namespace"] != "anchat" {
		t.Errorf("namespace %v, want anchat", body["namespace"])
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "anchat") {
		t.Errorf("the message %q does not name the namespace", msg)
	}
}

// The whole point is that no credential comes back. A response that refuses in
// the status line and hands over a key in the body has refused nothing.
func TestWriteCredentialError_carriesNoCredential(t *testing.T) {
	rec := httptest.NewRecorder()

	writeCredentialError(rec, "anchat", &authsvc.ErrNamespaceOwnedByAnother{Namespace: "anchat"})

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	for _, field := range []string{"api_key", "access_token", "refresh_token", "token"} {
		if _, present := body[field]; present {
			t.Errorf("the refusal carries %s", field)
		}
	}
}

// Anything else really is a server fault, and reporting it as 403 would tell a
// caller to stop retrying something that might succeed.
func TestWriteCredentialError_otherFailuresStay500(t *testing.T) {
	rec := httptest.NewRecorder()

	writeCredentialError(rec, "anchat", fmt.Errorf("rqlite unreachable"))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if _, present := body["code"]; present {
		t.Error("a server fault was given the not-owned code")
	}
}

// Every handler that asks for a credential has to route its failure through
// writeCredentialError. There are five such call sites across four handlers,
// and one of them reporting the refusal as a 500 would leave a caller retrying
// a namespace that will never be theirs.
//
// This walks the package rather than trusting a comment, so a sixth call site
// cannot be added without either handling it or failing here.
func TestEveryCredentialCallSiteReportsRefusalProperly(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse the handlers package: %v", err)
	}

	credentialCalls := map[string]bool{
		"GetOrCreateAPIKey":     true,
		"RequireNamespaceOwner": true,
	}

	var offenders []string
	found := 0

	for _, pkg := range pkgs {
		for path, file := range pkg.Files {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			ast.Inspect(file, func(n ast.Node) bool {
				// The shape is: x, err := <recv>.<Credential>(...) followed by
				// an `if err != nil` whose body reports the failure.
				block, ok := n.(*ast.BlockStmt)
				if !ok {
					return true
				}
				for i, stmt := range block.List {
					name := credentialCallName(stmt, credentialCalls)
					if name == "" {
						continue
					}
					found++
					if i+1 >= len(block.List) {
						offenders = append(offenders,
							fmt.Sprintf("%s: %s is called and its error is not checked at all",
								fset.Position(stmt.Pos()), name))
						continue
					}
					handler, ok := block.List[i+1].(*ast.IfStmt)
					if !ok {
						offenders = append(offenders,
							fmt.Sprintf("%s: %s is not followed by an error check",
								fset.Position(stmt.Pos()), name))
						continue
					}
					if !mentions(handler.Body, "writeCredentialError") {
						offenders = append(offenders,
							fmt.Sprintf("%s: %s reports its failure without writeCredentialError, "+
								"so a namespace owned by another wallet comes back as a server error",
								fset.Position(stmt.Pos()), name))
					}
				}
				return true
			})
		}
	}

	if found == 0 {
		t.Fatal("no credential call sites found — the walk is broken, not the code")
	}
	for _, o := range offenders {
		t.Error(o)
	}
}

// credentialCallName returns the credential method a statement calls, or "".
func credentialCallName(stmt ast.Stmt, wanted map[string]bool) string {
	var call *ast.CallExpr
	switch s := stmt.(type) {
	case *ast.AssignStmt:
		if len(s.Rhs) == 1 {
			call, _ = s.Rhs[0].(*ast.CallExpr)
		}
	case *ast.ExprStmt:
		call, _ = s.X.(*ast.CallExpr)
	}
	if call == nil {
		return ""
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || !wanted[sel.Sel.Name] {
		return ""
	}
	return sel.Sel.Name
}

// mentions reports whether an identifier appears anywhere in a node.
func mentions(n ast.Node, name string) bool {
	found := false
	ast.Inspect(n, func(node ast.Node) bool {
		if ident, ok := node.(*ast.Ident); ok && ident.Name == name {
			found = true
			return false
		}
		return !found
	})
	return found
}

// The ownership check has to come before anything is issued or provisioned.
//
// Refusing only at the point the key is minted still leaves the damage behind:
// VerifyHandler issues a JWT and a refresh-token row first, and the api-key
// handler can trigger cluster provisioning for a namespace the caller does not
// own. Neither is undone by the 403 that follows.
func TestOwnershipIsCheckedBeforeAnythingIsIssued(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse the handlers package: %v", err)
	}

	// Handlers that take a wallet signature and hand back credentials.
	gated := map[string]bool{
		"VerifyHandler":      true,
		"IssueAPIKeyHandler": true,
	}
	// Things that must not happen before the caller is known to be the owner.
	costly := map[string]bool{
		"IssueTokens":               true,
		"ProvisionNamespaceCluster": true,
		"GetOrCreateAPIKey":         true,
	}

	checked := map[string]bool{}

	for _, pkg := range pkgs {
		for path, file := range pkg.Files {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || !gated[fn.Name.Name] {
					continue
				}
				checked[fn.Name.Name] = true

				gate := token.NoPos
				firstCostly := token.NoPos
				firstCostlyName := ""
				ast.Inspect(fn, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					sel, ok := call.Fun.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					if sel.Sel.Name == "RequireNamespaceOwner" && gate == token.NoPos {
						gate = call.Pos()
					}
					if costly[sel.Sel.Name] && firstCostly == token.NoPos {
						firstCostly, firstCostlyName = call.Pos(), sel.Sel.Name
					}
					return true
				})

				if gate == token.NoPos {
					t.Errorf("%s never calls RequireNamespaceOwner, so any wallet may sign in "+
						"to any namespace", fn.Name.Name)
					continue
				}
				if firstCostly != token.NoPos && firstCostly < gate {
					t.Errorf("%s calls %s at %s, before the ownership check at %s — a wallet that "+
						"does not own the namespace leaves that behind",
						fn.Name.Name, firstCostlyName,
						fset.Position(firstCostly), fset.Position(gate))
				}
			}
		}
	}

	for name := range gated {
		if !checked[name] {
			t.Errorf("%s was not found in this package; the walk is looking for the wrong name", name)
		}
	}
}
