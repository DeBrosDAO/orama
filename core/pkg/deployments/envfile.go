package deployments

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// A deployment's environment is the tenant's, and its values are arbitrary
// text. They used to be interpolated straight into the systemd unit as
// Environment="{{.}}", so a value carrying a double quote and a newline closed
// the assignment and wrote whatever unit directives it liked — into a unit that
// ran as root. The values belong in an EnvironmentFile, encoded so that nothing
// in them can mean anything to systemd.
//
// The encoding below is read off systemd's own parser
// (src/basic/env-file.c, parse_env_file_internal). Inside a double-quoted value
// that parser copies every byte literally except:
//
//   - '"' , which closes the value
//   - '\' , which starts an escape
//
// and in the escape state a character in SHELL_NEED_ESCAPE (`"`, `\`, '`', '$'
// — src/basic/escape.h) is emitted as itself, a newline is swallowed as a line
// continuation, and any other character keeps its backslash.
//
// So double-quoting the value and escaping exactly SHELL_NEED_ESCAPE is
// faithful for every other byte, newlines and spaces included. Unquoted and
// single-quoted forms are not: unquoted values lose backslashes and have their
// surrounding whitespace stripped, and a single-quoted value has no escape at
// all, so it cannot carry a single quote.
const shellNeedEscape = "\"\\`$"

// MaxEnvValueBytes caps one environment value.
//
// The values are replicated through Raft to every node in the cluster and read
// back on every deploy, restart and reconfigure. There was no ceiling, so one
// deployment could put a hundred megabytes into the cluster's log.
const MaxEnvValueBytes = 64 * 1024

// ValidateEnvName reports whether key is usable as an environment variable
// name. The names become the left-hand side of an EnvironmentFile assignment,
// where a name carrying '=' or a newline would write a line the file did not
// intend.
func ValidateEnvName(key string) error {
	if key == "" {
		return fmt.Errorf("an environment variable name cannot be empty")
	}
	for i, r := range key {
		isLetter := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		isDigit := r >= '0' && r <= '9'
		if isLetter || r == '_' || (isDigit && i > 0) {
			continue
		}
		return fmt.Errorf("invalid environment variable name %q: use letters, digits and underscore, not starting with a digit", key)
	}
	return nil
}

// ValidateEnvValue reports whether value survives the round trip to the
// process's environment.
//
// systemd drops an assignment whose value is not valid UTF-8 (env-file.c,
// check_utf8ness_and_warn) and does so with a log line the tenant never sees,
// so the variable would simply be missing at runtime. A NUL cannot be carried
// in a POSIX environment at all. Both are refused where they are set rather
// than silently lost where they are used.
func ValidateEnvValue(key, value string) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("the value of %s is not valid UTF-8, and systemd discards such a variable instead of passing it to the process", key)
	}
	if strings.ContainsRune(value, 0) {
		return fmt.Errorf("the value of %s contains a NUL byte, which a process environment cannot carry", key)
	}
	if len(value) > MaxEnvValueBytes {
		return fmt.Errorf("the value of %s is %d bytes, over the %d-byte limit; every environment value is replicated to every node in the cluster",
			key, len(value), MaxEnvValueBytes)
	}
	return nil
}

// ValidateEnv checks every name and value in env.
func ValidateEnv(env map[string]string) error {
	for _, key := range sortedEnvKeys(env) {
		if err := ValidateEnvName(key); err != nil {
			return err
		}
		if err := ValidateEnvValue(key, env[key]); err != nil {
			return err
		}
	}
	return nil
}

// EncodeEnvFileValue returns value as a systemd EnvironmentFile right-hand
// side: the whole value double-quoted, with the four characters systemd treats
// as escapable inside double quotes escaped.
func EncodeEnvFileValue(value string) string {
	var b strings.Builder
	b.Grow(len(value) + 2)
	b.WriteByte('"')
	for i := 0; i < len(value); i++ {
		c := value[i]
		if strings.IndexByte(shellNeedEscape, c) >= 0 {
			b.WriteByte('\\')
		}
		b.WriteByte(c)
	}
	b.WriteByte('"')
	return b.String()
}

// RenderEnvFile returns the contents of a systemd EnvironmentFile for env.
//
// Keys are emitted in sorted order so that an unchanged environment renders
// byte-identical, and a rewritten unit does not look like a change.
func RenderEnvFile(env map[string]string) (string, error) {
	if err := ValidateEnv(env); err != nil {
		return "", err
	}
	var b strings.Builder
	for _, key := range sortedEnvKeys(env) {
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(EncodeEnvFileValue(env[key]))
		b.WriteByte('\n')
	}
	return b.String(), nil
}

func sortedEnvKeys(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
