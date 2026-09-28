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

// MaxEnvFileBytes caps a tenant's environment, measured as the file it
// renders to.
//
// orama-privhelper refuses to stage an environment file over its own limit
// (privhelper.MaxDeploySecretBytes, 256 KiB), and the file it stages is the
// tenant's variables plus the platform's. This cap is on the tenant's part
// alone, where it is set and stored, and leaves room for the platform's — so
// an environment that is accepted can always be started. A test holds the two
// together.
const MaxEnvFileBytes = 224 * 1024

// ValidateEnvSize refuses a tenant environment over MaxEnvFileBytes. It is for
// the tenant's own variables — where they are set and stored — not for the
// merged environment the process manager renders.
func ValidateEnvSize(env map[string]string) error {
	size := 0
	for key, value := range env {
		size += len(key) + len("=") + len(EncodeEnvFileValue(value)) + len("\n")
	}
	if size > MaxEnvFileBytes {
		return fmt.Errorf("the environment renders to %d bytes, over the %d-byte limit for a deployment's environment", size, MaxEnvFileBytes)
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

// ParseEnvFile reads back what RenderEnvFile wrote, and nothing else: one
// KEY="value" assignment per line, the value escaped as EncodeEnvFileValue
// escapes it.
//
// A tenant's value may hold newlines, so a line that reads PORT=... can be
// the inside of another variable's value; only a parser that follows the
// quoting knows where each assignment ends. Anything RenderEnvFile cannot
// have written — a bare backslash, an unquoted value, a key given twice, text
// after the closing quote — is an error rather than a guess.
func ParseEnvFile(contents string) (map[string]string, error) {
	// Errors name a byte offset, never the file's text: the values are the
	// tenant's secrets, and a malformed file is reported in operator output.
	env := map[string]string{}
	rest := contents
	for rest != "" {
		at := len(contents) - len(rest)
		eq := strings.IndexByte(rest, '=')
		if eq < 0 || ValidateEnvName(rest[:eq]) != nil {
			return nil, fmt.Errorf("environment file: byte %d does not start a KEY=\"value\" assignment", at)
		}
		key := rest[:eq]
		if _, dup := env[key]; dup {
			return nil, fmt.Errorf("environment file: %s is assigned twice", key)
		}
		value, n, err := decodeEnvFileValue(rest[eq+1:])
		if err != nil {
			return nil, fmt.Errorf("environment file: the value of %s at byte %d: %w", key, at, err)
		}
		env[key] = value
		rest = rest[eq+1+n:]
	}
	return env, nil
}

// decodeEnvFileValue decodes one quoted value and the newline that ends its
// assignment, returning the value and how many bytes of s it used.
func decodeEnvFileValue(s string) (string, int, error) {
	if s == "" || s[0] != '"' {
		return "", 0, fmt.Errorf("it is not double-quoted")
	}
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		switch c := s[i]; c {
		case '\\':
			if i+1 == len(s) || strings.IndexByte(shellNeedEscape, s[i+1]) < 0 {
				return "", 0, fmt.Errorf("it has a backslash that escapes nothing")
			}
			i++
			b.WriteByte(s[i])
		case '"':
			if i+1 == len(s) || s[i+1] != '\n' {
				return "", 0, fmt.Errorf("its closing quote is not followed by the end of the line")
			}
			return b.String(), i + 2, nil
		default:
			b.WriteByte(c)
		}
	}
	return "", 0, fmt.Errorf("its quote is never closed")
}
