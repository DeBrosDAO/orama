package shared

import (
	"errors"
	"testing"
)

// A failing gateway answers in two shapes. These endpoints use http.Error,
// which writes plain text; others answer with a JSON object. Both have to reach
// the operator as the same one-line reason, or a CLI error reads as a JSON dump.

func TestGatewayMessage_plain_text(t *testing.T) {
	if got := gatewayMessage([]byte("Domain already in use\n")); got != "Domain already in use" {
		t.Errorf("got %q, want the trimmed text", got)
	}
}

func TestGatewayMessage_json_error_field(t *testing.T) {
	if got := gatewayMessage([]byte(`{"error":"deployment not found"}`)); got != "deployment not found" {
		t.Errorf("got %q, want the error field", got)
	}
}

func TestGatewayMessage_json_message_field(t *testing.T) {
	if got := gatewayMessage([]byte(`{"message":"rate limited"}`)); got != "rate limited" {
		t.Errorf("got %q, want the message field", got)
	}
}

// An error field wins: a reply carrying both is describing the failure in the
// first and the outcome in the second.
func TestGatewayMessage_prefers_error_over_message(t *testing.T) {
	if got := gatewayMessage([]byte(`{"error":"bad token","message":"ok"}`)); got != "bad token" {
		t.Errorf("got %q, want the error field", got)
	}
}

func TestGatewayMessage_empty_body(t *testing.T) {
	if got := gatewayMessage([]byte("   \n")); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// A JSON body with neither field is still the most useful thing to show.
func TestGatewayMessage_json_without_either_field(t *testing.T) {
	body := `{"code":42}`
	if got := gatewayMessage([]byte(body)); got != body {
		t.Errorf("got %q, want the body verbatim", got)
	}
}

func TestStatusError_message(t *testing.T) {
	err := &StatusError{Status: 404, Message: "Domain not found"}
	if got := err.Error(); got != "Domain not found (HTTP 404)" {
		t.Errorf("got %q", got)
	}
}

func TestStatusError_without_a_message_still_names_the_status(t *testing.T) {
	if got := (&StatusError{Status: 502}).Error(); got != "gateway returned HTTP 502" {
		t.Errorf("got %q", got)
	}
}

// Callers branch on the status: a 400 from verify means "not visible yet" and
// is worth retrying, a 404 means the domain was never added.
func TestStatusOf_reads_the_status_through_a_wrap(t *testing.T) {
	wrapped := errors.Join(errors.New("verify"), &StatusError{Status: 400})
	if got := StatusOf(wrapped); got != 400 {
		t.Errorf("StatusOf = %d, want 400", got)
	}
}

func TestStatusOf_is_zero_for_an_ordinary_error(t *testing.T) {
	if got := StatusOf(errors.New("connection refused")); got != 0 {
		t.Errorf("StatusOf = %d, want 0 for an error carrying no status", got)
	}
}

func TestStatusOf_is_zero_for_nil(t *testing.T) {
	if got := StatusOf(nil); got != 0 {
		t.Errorf("StatusOf = %d, want 0", got)
	}
}
