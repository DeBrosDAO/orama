package deployments

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"
)

// envPairs turns --env KEY=VAL flags and an --env-file into one map.
//
// The file is read first so an explicit --env on the command line wins: that
// is the order people expect when overriding one value from a checked-in .env
// for a single deploy.
func envPairs(pairs []string, file string) (map[string]string, error) {
	env := map[string]string{}

	if file != "" {
		fromFile, err := readEnvFile(file)
		if err != nil {
			return nil, err
		}
		env = fromFile
	}

	for _, pair := range pairs {
		key, value, err := splitEnvPair(pair)
		if err != nil {
			return nil, err
		}
		env[key] = value
	}
	return env, nil
}

// splitEnvPair splits KEY=VALUE on the first '=' only, so a value may contain
// one — a connection string or a base64 secret routinely does.
func splitEnvPair(pair string) (string, string, error) {
	key, value, found := strings.Cut(pair, "=")
	if !found {
		return "", "", fmt.Errorf("--env %q is not KEY=VALUE", pair)
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", "", fmt.Errorf("--env %q has an empty name", pair)
	}
	return key, value, nil
}

// readEnvFile parses a .env file: KEY=VALUE per line, # comments, blank lines
// ignored, and one layer of surrounding quotes removed from the value.
//
// This is deliberately not a shell parser. It does not expand $VAR or run
// command substitution, because a deploy must send the literal bytes in the
// file rather than whatever the machine running the deploy happens to expand
// them to.
func readEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read env file: %w", err)
	}
	defer f.Close()

	env := map[string]string{}
	scanner := bufio.NewScanner(f)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		text = strings.TrimPrefix(text, "export ")

		key, value, found := strings.Cut(text, "=")
		if !found {
			return nil, fmt.Errorf("%s:%d: %q is not KEY=VALUE", path, line, text)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("%s:%d: empty variable name", path, line)
		}
		env[key] = unquote(strings.TrimSpace(value))
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read env file: %w", err)
	}
	return env, nil
}

// unquote strips one matching pair of surrounding quotes.
func unquote(s string) string {
	if len(s) < 2 {
		return s
	}
	first, last := s[0], s[len(s)-1]
	if first == last && (first == '"' || first == '\'') {
		return s[1 : len(s)-1]
	}
	return s
}

// addEnvFields writes each variable into the upload form as env_<NAME>.
//
// One field per variable rather than one encoded blob: a value containing an
// '=' or a newline then needs no escaping, and the server reads it back with
// no parser of its own.
func addEnvFields(form map[string]string, env map[string]string) {
	for key, value := range env {
		form["env_"+key] = value
	}
}

// sortedEnvKeys returns env's names in a stable order for printing.
func sortedEnvKeys(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
