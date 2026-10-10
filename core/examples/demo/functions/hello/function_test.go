package main

import (
	"encoding/json"
	"strings"
	"testing"

	"orama-demo/host"
)

func call(t *testing.T, h host.Host, input string) map[string]string {
	t.Helper()
	out, err := handle(h, []byte(input))
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("output %s is not a JSON object of strings: %v", out, err)
	}
	return got
}

func TestHandle_greetsTheNameAndNamesTheCaller(t *testing.T) {
	h := host.NewFake()
	h.Wallet = "0xabc"

	got := call(t, h, `{"name":"Ada"}`)

	if got["greeting"] != "Hello, Ada!" || got["caller"] != "0xabc" {
		t.Errorf("got %v", got)
	}
	if !h.Logged("hello for 0xabc") {
		t.Errorf("the call is not in the invocation log: %v", h.Logs)
	}
}

func TestHandle_anAnonymousCallerIsSaidToBe(t *testing.T) {
	got := call(t, host.NewFake(), `{"name":"Ada"}`)

	if got["caller"] != anonymous {
		t.Errorf("caller = %q, want %q", got["caller"], anonymous)
	}
}

func TestHandle_noInputOrNoNameGreetsTheWorld(t *testing.T) {
	for _, in := range []string{``, `{}`, `{"name":""}`, `{"name":"   "}`, `{"name":"\u0007\u0000"}`} {
		if got := call(t, host.NewFake(), in); got["greeting"] != "Hello, World!" {
			t.Errorf("input %q greeted %q", in, got["greeting"])
		}
	}
}

func TestHandle_aLongNameIsCut(t *testing.T) {
	got := call(t, host.NewFake(), `{"name":"`+strings.Repeat("é", 500)+`"}`)

	name := strings.TrimSuffix(strings.TrimPrefix(got["greeting"], "Hello, "), "!")
	if n := len([]rune(name)); n != maxNameRunes {
		t.Errorf("greeted a name of %d characters, want %d", n, maxNameRunes)
	}
}

func TestHandle_controlCharactersAreDropped(t *testing.T) {
	got := call(t, host.NewFake(), `{"name":"Ad\u001b[31ma\n"}`)

	if strings.ContainsAny(got["greeting"], "\x1b\n") {
		t.Errorf("greeting %q kept control characters", got["greeting"])
	}
}

func TestHandle_inputThatIsNotJSONIsAnAnswerNotAFailure(t *testing.T) {
	out, err := handle(host.NewFake(), []byte(`name=Ada`))

	if err != nil {
		t.Fatalf("handle: %v: a caller's mistake is not a failure of the function", err)
	}
	if !strings.Contains(string(out), `"error"`) {
		t.Errorf("output %s does not say what was wrong", out)
	}
}
