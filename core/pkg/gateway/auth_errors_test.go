package gateway

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A 401 had at least six causes and told them apart only by an English string,
// so a client could not tell "you sent nothing" from "your key was revoked"
// from "your token expired" without matching on prose. Every refusal carries a
// code now.

func decodeRefusal(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body
}

func TestWriteAuthError_carriesACodeAMessageAndAHint(t *testing.T) {
	rec := httptest.NewRecorder()
	unauthorized(rec, CodeAuthMissing, "no credential was presented", nil)

	body := decodeRefusal(t, rec)
	if body["code"] != CodeAuthMissing {
		t.Errorf("code = %v", body["code"])
	}
	if body["error"] != "no credential was presented" {
		t.Errorf("error = %v", body["error"])
	}
	if hint, _ := body["hint"].(string); hint == "" {
		t.Error("no hint: the code says what happened, the hint says what to do about it")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d", rec.Code)
	}
}

// A 401 has to say how to authenticate, or a browser will not offer to.
func TestWriteAuthError_a401CarriesWWWAuthenticate(t *testing.T) {
	rec := httptest.NewRecorder()
	unauthorized(rec, CodeAuthInvalidKey, "nope", nil)
	if rec.Header().Get("WWW-Authenticate") == "" {
		t.Error("a 401 with no WWW-Authenticate")
	}

	forbiddenRec := httptest.NewRecorder()
	forbidden(forbiddenRec, CodeOwnershipRequired, "nope", nil)
	if forbiddenRec.Header().Get("WWW-Authenticate") != "" {
		t.Error("a 403 carries WWW-Authenticate, which invites a client to retry with a different credential for a decision that is not about the credential")
	}
}

// The cause-specific fields are what make the code actionable: which grant,
// which namespace.
func TestWriteAuthError_carriesTheCauseSpecificFields(t *testing.T) {
	rec := httptest.NewRecorder()
	forbidden(rec, CodeScopeMissing, "lacks the grant", map[string]any{"required_scope": "admin"})

	body := decodeRefusal(t, rec)
	if body["required_scope"] != "admin" {
		t.Errorf("required_scope = %v; a client that has to regex the message cannot act on it", body["required_scope"])
	}
}

// Every code has a hint. A code with none is a refusal that says what happened
// and not what to do.
func TestAuthHints_coverEveryCode(t *testing.T) {
	for _, code := range []string{
		CodeAuthMissing, CodeAuthInvalidKey, CodeAuthRevoked, CodeAuthExpired,
		CodeAuthUserJWTRequired, CodeScopeMissing, CodeNamespaceMismatch,
		CodeOwnershipRequired, CodeOperatorRequired, CodeDestinationNotAllowed,
	} {
		if authHints[code] == "" {
			t.Errorf("%s has no hint", code)
		}
	}
}

// Two codes meaning different things must not share a wire value, or a client
// switching on it cannot tell them apart.
func TestAuthCodes_areDistinct(t *testing.T) {
	codes := map[string]string{
		"CodeAuthMissing":           CodeAuthMissing,
		"CodeAuthInvalidKey":        CodeAuthInvalidKey,
		"CodeAuthRevoked":           CodeAuthRevoked,
		"CodeAuthExpired":           CodeAuthExpired,
		"CodeAuthUserJWTRequired":   CodeAuthUserJWTRequired,
		"CodeScopeMissing":          CodeScopeMissing,
		"CodeNamespaceMismatch":     CodeNamespaceMismatch,
		"CodeOwnershipRequired":     CodeOwnershipRequired,
		"CodeOperatorRequired":      CodeOperatorRequired,
		"CodeDestinationNotAllowed": CodeDestinationNotAllowed,
	}
	seen := map[string]string{}
	for name, value := range codes {
		if other, dup := seen[value]; dup {
			t.Errorf("%s and %s are both %q", name, other, value)
		}
		seen[value] = name
	}
}

// The two codes that were already on the wire keep their spelling: the SDK
// turns INSUFFICIENT_SCOPE into a ScopeError, and renaming would break every
// client that switches on it.
func TestAuthCodes_keepTheSpellingsAlreadyShipped(t *testing.T) {
	if CodeScopeMissing != "INSUFFICIENT_SCOPE" {
		t.Errorf("CodeScopeMissing = %q; the SDK switches on INSUFFICIENT_SCOPE", CodeScopeMissing)
	}
	if CodeAuthUserJWTRequired != "USER_JWT_REQUIRED" {
		t.Errorf("CodeAuthUserJWTRequired = %q", CodeAuthUserJWTRequired)
	}
	if CodeOperatorRequired != "NOT_AN_OPERATOR" {
		t.Errorf("CodeOperatorRequired = %q", CodeOperatorRequired)
	}
}

// A refusal written with the plain error writer carries no code, which is the
// state this replaced. The two shapes exist so that cannot happen by accident.
func TestRefusals_allGoThroughTheCodedWriter(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	found := 0
	for _, pkg := range pkgs {
		for path, file := range pkg.Files {
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				name, ok := call.Fun.(*ast.Ident)
				if !ok || (name.Name != "writeError" && name.Name != "writeJSON") {
					return true
				}
				// The status argument, when it is a refusal.
				if len(call.Args) < 2 {
					return true
				}
				sel, ok := call.Args[1].(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if sel.Sel.Name != "StatusUnauthorized" && sel.Sel.Name != "StatusForbidden" {
					return true
				}
				found++
				pos := fset.Position(call.Pos())
				t.Errorf("%s:%d writes a %s without a code. Use unauthorized() or forbidden() so the "+
					"caller can tell this refusal from the five others that look the same.",
					path[strings.LastIndexByte(path, '/')+1:], pos.Line, sel.Sel.Name)
				return true
			})
		}
	}
	_ = found
}
