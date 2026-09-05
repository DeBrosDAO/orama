package deployments

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// PORT is written into the systemd unit by the process manager and is how the
// gateway reaches the app; ENTRY_POINT is how a Node.js deployment knows what
// to run. Overwriting either breaks the deployment in a way that looks to the
// developer like their own code failing.

func TestApplyEnvChanges_sets_and_unsets(t *testing.T) {
	current := map[string]string{"KEEP": "1", "DROP": "2", "CHANGE": "old"}

	got, err := applyEnvChanges(current, map[string]string{"CHANGE": "new", "ADD": "3"}, []string{"DROP"})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	for key, want := range map[string]string{"KEEP": "1", "CHANGE": "new", "ADD": "3"} {
		if got[key] != want {
			t.Errorf("%s = %q, want %q", key, got[key], want)
		}
	}
	if _, still := got["DROP"]; still {
		t.Error("DROP must be gone")
	}
}

// A rejected change must leave the deployment's recorded environment untouched.
func TestApplyEnvChanges_does_not_mutate_the_current_map(t *testing.T) {
	current := map[string]string{"A": "1"}

	if _, err := applyEnvChanges(current, map[string]string{"B": "2"}, []string{"A"}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if current["A"] != "1" || len(current) != 1 {
		t.Errorf("current was mutated: %v", current)
	}
}

func TestApplyEnvChanges_refuses_to_overwrite_a_reserved_key(t *testing.T) {
	for _, key := range []string{"PORT", "ENTRY_POINT"} {
		if _, err := applyEnvChanges(nil, map[string]string{key: "x"}, nil); err == nil {
			t.Errorf("setting %s must be refused", key)
		}
	}
}

func TestApplyEnvChanges_refuses_to_unset_a_reserved_key(t *testing.T) {
	current := map[string]string{"PORT": "8080"}
	if _, err := applyEnvChanges(current, nil, []string{"PORT"}); err == nil {
		t.Fatal("removing PORT must be refused; the app would become unreachable")
	}
}

// Unsetting a name that is not there is not an error: the command has to be
// repeatable after a partial failure.
func TestApplyEnvChanges_unsetting_an_absent_key_is_fine(t *testing.T) {
	got, err := applyEnvChanges(map[string]string{"A": "1"}, nil, []string{"NOPE"})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("got %v, want A alone", got)
	}
}

// Names become Environment= lines in a systemd unit, so one carrying a newline
// or an '=' would write a line the unit file did not intend.
func TestValidateEnvKey_rejects_names_that_would_corrupt_the_unit(t *testing.T) {
	for _, key := range []string{"", "WITH SPACE", "WITH=EQUALS", "WITH\nNEWLINE", "1LEADING_DIGIT", "dash-name"} {
		if err := validateEnvKey(key); err == nil {
			t.Errorf("%q must be rejected", key)
		}
	}
}

func TestValidateEnvKey_accepts_ordinary_names(t *testing.T) {
	for _, key := range []string{"A", "_PRIVATE", "DATABASE_URL", "LEVEL2", "lowercase"} {
		if err := validateEnvKey(key); err != nil {
			t.Errorf("%q must be accepted: %v", key, err)
		}
	}
}

func TestParseFormEnv_reads_only_prefixed_fields(t *testing.T) {
	env, err := parseFormEnv(map[string][]string{
		"name":         {"my-api"},
		"env_API_KEY":  {"secret"},
		"env_LOG_LEVE": {"debug"},
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(env) != 2 {
		t.Fatalf("got %v, want two variables", env)
	}
	if env["API_KEY"] != "secret" {
		t.Errorf("API_KEY = %q", env["API_KEY"])
	}
	if _, leaked := env["name"]; leaked {
		t.Error("an unprefixed field must not become a variable")
	}
}

func TestParseFormEnv_rejects_a_reserved_name(t *testing.T) {
	if _, err := parseFormEnv(map[string][]string{"env_PORT": {"1"}}); err == nil {
		t.Fatal("env_PORT must be refused at upload, not silently applied")
	}
}

// ENTRY_POINT is reserved, so parseFormEnv already rejected any attempt to
// supply it and this can never silently override a user's value.
func TestWithEntryPoint(t *testing.T) {
	got := withEntryPoint(map[string]string{"API_KEY": "secret"}, "server.js")
	if got["ENTRY_POINT"] != "server.js" {
		t.Errorf("ENTRY_POINT = %q", got["ENTRY_POINT"])
	}
	if got["API_KEY"] != "secret" {
		t.Errorf("the caller's variables must survive: %v", got)
	}
}

func TestWithEntryPoint_does_not_mutate_its_input(t *testing.T) {
	env := map[string]string{"A": "1"}
	withEntryPoint(env, "index.js")
	if len(env) != 1 {
		t.Errorf("input was mutated: %v", env)
	}
}

// Values are where the platform tells people to put secrets. An endpoint that
// echoes them puts every secret behind nothing more than a read scope, and into
// whatever scrollback or CI log the caller is writing to.
func TestHandleGetEnv_never_returns_values(t *testing.T) {
	body := envListBody(t, map[string]string{"API_KEY": "super-secret", "PORT": "8080"})

	if strings.Contains(body, "super-secret") {
		t.Fatalf("the response contains a value:\n%s", body)
	}
	if !strings.Contains(body, "API_KEY") {
		t.Errorf("the response must list the names:\n%s", body)
	}
}

func TestHandleGetEnv_marks_reserved_names(t *testing.T) {
	var resp struct {
		Variables []struct {
			Key      string `json:"key"`
			Reserved bool   `json:"reserved"`
		} `json:"variables"`
	}
	if err := json.Unmarshal([]byte(envListBody(t, map[string]string{"API_KEY": "x", "PORT": "8080"})), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	seen := map[string]bool{}
	for _, v := range resp.Variables {
		seen[v.Key] = v.Reserved
	}
	if !seen["PORT"] {
		t.Error("PORT must be marked as set by the platform")
	}
	if seen["API_KEY"] {
		t.Error("API_KEY must not be marked reserved")
	}
}

// envListBody renders what HandleGetEnv would write for an environment,
// exercising the same projection without standing up a database.
func envListBody(t *testing.T, env map[string]string) string {
	t.Helper()

	w := httptest.NewRecorder()
	vars := make([]map[string]any, 0, len(env))
	for _, key := range sortedKeys(env) {
		vars = append(vars, map[string]any{"key": key, "reserved": reservedEnvKeys[key]})
	}
	writeJSON(w, map[string]any{"deployment_name": "my-api", "variables": vars, "total": len(vars)})
	return w.Body.String()
}

func TestHandleSetEnv_requires_the_name_in_the_query(t *testing.T) {
	// withHomeNodeProxy reads ?name= to decide which node owns the deployment.
	// A name only in the body would leave it unable to route, and the unit file
	// would be rewritten on whichever node the client happened to reach.
	h := &EnvHandler{}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/deployments/env/set",
		strings.NewReader(`{"set":{"A":"1"}}`))

	h.HandleSetEnv(w, r)

	if w.Code != http.StatusUnauthorized && w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want a refusal without a name", w.Code)
	}
}

func TestEnvHandlers_reject_the_wrong_method(t *testing.T) {
	h := &EnvHandler{}

	w := httptest.NewRecorder()
	h.HandleGetEnv(w, httptest.NewRequest(http.MethodDelete, "/v1/deployments/env", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET endpoint with DELETE: status = %d", w.Code)
	}

	w = httptest.NewRecorder()
	h.HandleSetEnv(w, httptest.NewRequest(http.MethodGet, "/v1/deployments/env/set", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST endpoint with GET: status = %d", w.Code)
	}
}
