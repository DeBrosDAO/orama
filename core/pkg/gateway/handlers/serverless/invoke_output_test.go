package serverless

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/serverless"
)

// Output that is JSON is the function's answer, whatever its first byte. Only
// '{' and '[' used to count, so null — what a function returns from a refused
// database call — came back wrapped in an envelope that read as a result.
func TestWriteInvocationOutput_anyJSONValueIsReturnedAsItIs(t *testing.T) {
	for _, out := range []string{`null`, `42`, `"text"`, `true`, `{"a":1}`, `[1,2]`} {
		w := httptest.NewRecorder()
		writeInvocationOutput(w, &serverless.InvokeResponse{RequestID: "r1", Output: []byte(out)})
		if got := w.Body.String(); got != out {
			t.Errorf("output %s came back as %s", out, got)
		}
		if ct := w.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("output %s: content type %q", out, ct)
		}
	}
}

// Output that is not JSON is wrapped, with the request id it belongs to.
func TestWriteInvocationOutput_textIsWrapped(t *testing.T) {
	for _, out := range []string{`hello`, ``, `{not json`} {
		w := httptest.NewRecorder()
		writeInvocationOutput(w, &serverless.InvokeResponse{RequestID: "r1", Output: []byte(out), Status: "success"})
		var env map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatalf("output %q: the answer is not JSON: %s", out, w.Body.String())
		}
		if env["output"] != out || env["request_id"] != "r1" || !strings.EqualFold(env["status"].(string), "success") {
			t.Errorf("output %q was wrapped as %v", out, env)
		}
	}
}
