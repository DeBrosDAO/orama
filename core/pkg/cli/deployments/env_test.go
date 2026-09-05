package deployments

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A value routinely contains an '=' — a connection string, a base64 secret, a
// JWT. Splitting on every '=' would truncate all three.
func TestSplitEnvPair_splits_on_the_first_equals_only(t *testing.T) {
	key, value, err := splitEnvPair("DATABASE_URL=postgres://u:p@h/db?sslmode=require")
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if key != "DATABASE_URL" {
		t.Errorf("key = %q", key)
	}
	if value != "postgres://u:p@h/db?sslmode=require" {
		t.Errorf("value = %q, want the whole URL", value)
	}
}

func TestSplitEnvPair_allows_an_empty_value(t *testing.T) {
	key, value, err := splitEnvPair("FEATURE_FLAG=")
	if err != nil {
		t.Fatalf("an empty value is legal: %v", err)
	}
	if key != "FEATURE_FLAG" || value != "" {
		t.Errorf("got %q=%q", key, value)
	}
}

func TestSplitEnvPair_rejects_a_bare_name(t *testing.T) {
	if _, _, err := splitEnvPair("DATABASE_URL"); err == nil {
		t.Fatal("KEY without =VALUE must be an error, not a variable set to empty")
	}
}

func TestSplitEnvPair_rejects_an_empty_name(t *testing.T) {
	if _, _, err := splitEnvPair("=value"); err == nil {
		t.Fatal("an empty name must be an error")
	}
}

func writeEnvFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write env file: %v", err)
	}
	return path
}

func TestReadEnvFile(t *testing.T) {
	path := writeEnvFile(t, `# a comment
DATABASE_URL=postgres://localhost/db

export API_KEY=secret
QUOTED="has spaces"
SINGLE='also quoted'
EMPTY=
`)
	env, err := readEnvFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	for _, tc := range []struct{ key, want string }{
		{"DATABASE_URL", "postgres://localhost/db"},
		{"API_KEY", "secret"},
		{"QUOTED", "has spaces"},
		{"SINGLE", "also quoted"},
		{"EMPTY", ""},
	} {
		if got := env[tc.key]; got != tc.want {
			t.Errorf("%s = %q, want %q", tc.key, got, tc.want)
		}
	}
	if len(env) != 5 {
		t.Errorf("got %d variables, want 5; comments and blank lines must be skipped", len(env))
	}
}

// A deploy must send the literal bytes in the file. Expanding $VAR would send
// whatever the machine running the deploy happens to have set.
func TestReadEnvFile_does_not_expand_variables(t *testing.T) {
	t.Setenv("HOME", "/home/someone")
	env, err := readEnvFile(writeEnvFile(t, "DATA_DIR=$HOME/data\n"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if env["DATA_DIR"] != "$HOME/data" {
		t.Errorf("DATA_DIR = %q, want the literal text", env["DATA_DIR"])
	}
}

func TestReadEnvFile_reports_the_line_of_a_bad_entry(t *testing.T) {
	_, err := readEnvFile(writeEnvFile(t, "GOOD=1\nBROKEN\n"))
	if err == nil {
		t.Fatal("a line that is not KEY=VALUE must be an error")
	}
	if !strings.Contains(err.Error(), ":2:") {
		t.Errorf("error %q must name the line number", err)
	}
}

func TestReadEnvFile_missing_file(t *testing.T) {
	if _, err := readEnvFile("/nonexistent/.env"); err == nil {
		t.Fatal("a missing env file must be an error, not an empty environment")
	}
}

// The file is the baseline; an explicit --env overrides one value for a single
// deploy without editing a checked-in file.
func TestEnvPairs_flags_override_the_file(t *testing.T) {
	path := writeEnvFile(t, "LOG_LEVEL=info\nAPI_KEY=from-file\n")

	env, err := envPairs([]string{"LOG_LEVEL=debug"}, path)
	if err != nil {
		t.Fatalf("envPairs: %v", err)
	}
	if env["LOG_LEVEL"] != "debug" {
		t.Errorf("LOG_LEVEL = %q, want the flag to win", env["LOG_LEVEL"])
	}
	if env["API_KEY"] != "from-file" {
		t.Errorf("API_KEY = %q, want the file value to survive", env["API_KEY"])
	}
}

func TestEnvPairs_with_neither_source(t *testing.T) {
	env, err := envPairs(nil, "")
	if err != nil {
		t.Fatalf("no env is not an error: %v", err)
	}
	if len(env) != 0 {
		t.Errorf("got %d variables, want 0", len(env))
	}
}

// One form field per variable, so a value with an '=' or a newline needs no
// escaping and the server needs no parser of its own.
func TestAddEnvFields_prefixes_each_name(t *testing.T) {
	form := map[string]string{"name": "my-api"}
	addEnvFields(form, map[string]string{"API_KEY": "a=b", "LOG": "x\ny"})

	if form["env_API_KEY"] != "a=b" {
		t.Errorf("env_API_KEY = %q", form["env_API_KEY"])
	}
	if form["env_LOG"] != "x\ny" {
		t.Errorf("env_LOG = %q, want the newline preserved", form["env_LOG"])
	}
	if form["name"] != "my-api" {
		t.Error("existing fields must survive")
	}
}

func TestSortedEnvKeys_is_stable(t *testing.T) {
	got := sortedEnvKeys(map[string]string{"C": "", "A": "", "B": ""})
	want := []string{"A", "B", "C"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
