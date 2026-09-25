package serverless

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/serverless"
	"github.com/DeBrosOfficial/network/pkg/serverless/triggers"
	"go.uber.org/zap"
)

// asCredentialOf is r as the auth middleware leaves it for a credential of
// namespace ns.
func asCredentialOf(r *http.Request, ns string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), ctxkeys.NamespaceOverride, ns))
}

// recordingRegistry records what reached the registry's writes.
type recordingRegistry struct {
	*mockRegistry
	registered []*serverless.FunctionDefinition
	deleted    []string
}

func (r *recordingRegistry) Register(_ context.Context, def *serverless.FunctionDefinition, _ []byte) (*serverless.Function, error) {
	r.registered = append(r.registered, def)
	return nil, nil
}

func (r *recordingRegistry) Delete(_ context.Context, namespace, name string, _ int) error {
	r.deleted = append(r.deleted, namespace+"/"+name)
	return nil
}

// deployRequest is a multipart deploy with the given metadata JSON and form
// namespace (either may be empty).
func deployRequest(t *testing.T, metadata, formNamespace string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("name", "hello")
	if metadata != "" {
		_ = mw.WriteField("metadata", metadata)
	}
	if formNamespace != "" {
		_ = mw.WriteField("namespace", formNamespace)
	}
	part, err := mw.CreateFormFile("wasm", "function.wasm")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	_, _ = part.Write([]byte("\x00asm"))
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/functions", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

// bugboard #423: a credential of one namespace deployed into another by naming
// it. Every way of naming it is refused, and nothing reaches the registry.
func TestDeployFunction_refusesAnotherNamespace(t *testing.T) {
	for name, req := range map[string]*http.Request{
		"metadata": deployRequest(t, `{"namespace":"victim"}`, ""),
		"form":     deployRequest(t, "", "victim"),
		"query": func() *http.Request {
			r := deployRequest(t, "", "")
			r.URL.RawQuery = "namespace=victim"
			return r
		}(),
		"header": func() *http.Request {
			r := deployRequest(t, "", "")
			r.Header.Set(headerNamespace, "victim")
			return r
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			reg := &recordingRegistry{mockRegistry: newMockRegistry()}
			h := newTestHandlers(reg)
			rec := httptest.NewRecorder()

			h.DeployFunction(rec, asCredentialOf(req, "attacker"))

			if rec.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403: %s", rec.Code, rec.Body.String())
			}
			if len(reg.registered) != 0 {
				t.Errorf("a refused deploy reached the registry: %+v", reg.registered[0])
			}
		})
	}
}

func TestDeployFunction_deploysIntoTheCredentialsNamespace(t *testing.T) {
	reg := &recordingRegistry{mockRegistry: newMockRegistry()}
	h := newTestHandlers(reg)
	rec := httptest.NewRecorder()

	// A metadata namespace that agrees with the credential is accepted.
	h.DeployFunction(rec, asCredentialOf(deployRequest(t, `{"namespace":"tenant"}`, ""), "tenant"))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	if len(reg.registered) != 1 || reg.registered[0].Namespace != "tenant" {
		t.Fatalf("registered %+v, want one function in namespace tenant", reg.registered)
	}
}

// Without a credential namespace there is nothing to manage; the request's own
// naming is not a substitute.
func TestDeployFunction_noCredentialNamespaceIsRefused(t *testing.T) {
	reg := &recordingRegistry{mockRegistry: newMockRegistry()}
	h := newTestHandlers(reg)
	rec := httptest.NewRecorder()

	h.DeployFunction(rec, deployRequest(t, `{"namespace":"tenant"}`, ""))

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
	if len(reg.registered) != 0 {
		t.Error("a deploy without a credential namespace reached the registry")
	}
}

func TestDeleteFunction_refusesAnotherNamespace(t *testing.T) {
	reg := &recordingRegistry{mockRegistry: newMockRegistry()}
	h := newTestHandlers(reg)
	req := asCredentialOf(httptest.NewRequest(http.MethodDelete, "/v1/functions/hello?namespace=victim", nil), "attacker")
	rec := httptest.NewRecorder()

	h.DeleteFunction(rec, req, "hello", 0)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
	if len(reg.deleted) != 0 {
		t.Errorf("a refused delete reached the registry: %v", reg.deleted)
	}
}

func TestDeleteFunction_deletesInTheCredentialsNamespace(t *testing.T) {
	reg := &recordingRegistry{mockRegistry: newMockRegistry()}
	h := newTestHandlers(reg)
	req := asCredentialOf(httptest.NewRequest(http.MethodDelete, "/v1/functions/hello", nil), "tenant")
	rec := httptest.NewRecorder()

	h.DeleteFunction(rec, req, "hello", 0)

	if rec.Code != http.StatusOK || len(reg.deleted) != 1 || reg.deleted[0] != "tenant/hello" {
		t.Errorf("status %d, deleted %v; want 200 and tenant/hello", rec.Code, reg.deleted)
	}
}

func TestGetFunctionLogs_refusesAnotherNamespace(t *testing.T) {
	reg := newMockRegistry()
	reg.invocations = []serverless.Invocation{{ID: "victim-invocation"}}
	h := newTestHandlers(reg)
	req := asCredentialOf(httptest.NewRequest(http.MethodGet, "/v1/functions/hello/logs", nil), "attacker")
	req.Header.Set(headerNamespace, "victim")
	rec := httptest.NewRecorder()

	h.GetFunctionLogs(rec, req, "hello")

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "victim-invocation") {
		t.Error("a refused logs request returned the logs")
	}
}

func TestListFunctions_refusesAnotherNamespace(t *testing.T) {
	reg := newMockRegistry()
	reg.functions["victim/secret-fn"] = &serverless.Function{Name: "secret-fn", Namespace: "victim"}
	h := newTestHandlers(reg)
	req := asCredentialOf(httptest.NewRequest(http.MethodGet, "/v1/functions?namespace=victim", nil), "attacker")
	rec := httptest.NewRecorder()

	h.ListFunctions(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "secret-fn") {
		t.Error("a refused list returned another namespace's functions")
	}
}

func TestListFunctions_listsOnlyTheCredentialsNamespace(t *testing.T) {
	reg := newMockRegistry()
	reg.functions["tenant/mine"] = &serverless.Function{Name: "mine", Namespace: "tenant"}
	reg.functions["victim/theirs"] = &serverless.Function{Name: "theirs", Namespace: "victim"}
	h := newTestHandlers(reg)
	rec := httptest.NewRecorder()

	h.ListFunctions(rec, asCredentialOf(httptest.NewRequest(http.MethodGet, "/v1/functions", nil), "tenant"))

	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "mine") || strings.Contains(body, "theirs") {
		t.Errorf("status %d, body %s; want only tenant's function", rec.Code, body)
	}
}

// Every management route takes the credential's namespace; one that named
// another was served against it. Each is asked, through the router, about the
// victim's function and secrets, and each must refuse before touching them.
func TestManagementRoutes_refuseAnotherNamespace(t *testing.T) {
	logger := zap.NewNop()
	reg := newMockRegistry()
	reg.functions["victim/hello"] = &serverless.Function{ID: "fn-1", Name: "hello", Namespace: "victim"}
	secrets := newMockSecretsManager()
	secrets.secrets["victim"] = map[string]string{"VICTIM_SECRET": "value"}
	h := NewServerlessHandlers(nil, nil, reg, serverless.NewWSManager(logger),
		triggers.NewPubSubTriggerStore(nil, logger), triggers.NewCronTriggerStore(nil, logger),
		nil, nil, nil, secrets, nil, logger)

	for _, route := range []struct{ method, path, body string }{
		{http.MethodGet, "/v1/functions/hello", ""},
		{http.MethodGet, "/v1/functions/hello/versions", ""},
		{http.MethodPost, "/v1/functions/hello/disable", ""},
		{http.MethodPost, "/v1/functions/hello/enable", ""},
		{http.MethodPost, "/v1/functions/hello/triggers", `{"topic":"t"}`},
		{http.MethodGet, "/v1/functions/hello/triggers", ""},
		{http.MethodDelete, "/v1/functions/hello/triggers/trigger-1", ""},
		{http.MethodPut, "/v1/functions/secrets", `{"name":"VICTIM_SECRET","value":"overwritten"}`},
		{http.MethodGet, "/v1/functions/secrets", ""},
		{http.MethodDelete, "/v1/functions/secrets/VICTIM_SECRET", ""},
	} {
		req := asCredentialOf(httptest.NewRequest(route.method, route.path+"?namespace=victim",
			strings.NewReader(route.body)), "attacker")
		rec := httptest.NewRecorder()

		h.handleFunctionByName(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s: status %d, want 403: %s", route.method, route.path, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "VICTIM_SECRET") {
			t.Errorf("%s %s: the refusal carried the victim's data", route.method, route.path)
		}
	}
	if secrets.secrets["victim"]["VICTIM_SECRET"] != "value" {
		t.Error("a refused request changed the victim's secret")
	}
}

// The persistent WebSocket runs the credential's namespace's functions too.
func TestHandleWebSocket_refusesAnotherNamespace(t *testing.T) {
	reg := newMockRegistry()
	reg.functions["victim/rpc"] = &serverless.Function{Name: "rpc", Namespace: "victim", WSPersistent: true, IsPublic: true}
	h := newTestHandlers(reg)
	req := asCredentialOf(httptest.NewRequest(http.MethodGet, "/v1/functions/rpc/ws?namespace=victim", nil), "attacker")
	rec := httptest.NewRecorder()

	h.HandleWebSocket(rec, req, "rpc", 0)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

// An anonymous invocation that names no namespace used to run the "default"
// namespace's function of that name. It now has to say whose it means.
func TestInvokeFunction_anonymousWithoutNamespaceIsRefused(t *testing.T) {
	h := newTestHandlers(nil)
	rec := httptest.NewRecorder()

	h.InvokeFunction(rec, httptest.NewRequest(http.MethodPost, "/v1/functions/hello/invoke", nil), "hello", 0)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "/v1/invoke/") {
		t.Errorf("the refusal does not say how to name the namespace: %s", rec.Body.String())
	}
}

func TestInvokeNamespace_order(t *testing.T) {
	cases := []struct {
		name string
		req  *http.Request
		want string
	}{
		{"query names it", httptest.NewRequest(http.MethodPost, "/x?namespace=public-ns", nil), "public-ns"},
		{"credential's", asCredentialOf(httptest.NewRequest(http.MethodPost, "/x", nil), "tenant"), "tenant"},
		{"query over credential", asCredentialOf(httptest.NewRequest(http.MethodPost, "/x?namespace=public-ns", nil), "tenant"), "public-ns"},
		{"a header names nothing", func() *http.Request {
			r := httptest.NewRequest(http.MethodPost, "/x", nil)
			r.Header.Set(headerNamespace, "header-ns")
			return r
		}(), ""},
	}
	for _, tc := range cases {
		if got := invokeNamespace(tc.req); got != tc.want {
			t.Errorf("%s: invokeNamespace = %q, want %q", tc.name, got, tc.want)
		}
	}
}
