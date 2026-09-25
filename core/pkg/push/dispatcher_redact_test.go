package push

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

const (
	redactTestToken = "0123456789abcdef-secret-device-token"
	redactTestTopic = "upSecretTopic0123456789"
)

// A transport error's text is the request URL, which carries the device token
// — or, for a UnifiedPush endpoint posted to a fan-out node, a URL that holds
// the token's topic without holding the token. The per-device result goes back
// to the calling function, and for a push topic possibly to a sender who must
// never learn either, so neither the result nor the log may repeat them.
func TestSendToDevice_failureTextNeverCarriesTheToken(t *testing.T) {
	endpointToken := "https://push.example.com/" + redactTestTopic + "?up=1"
	cases := []struct {
		name, token string
		err         error
	}{
		{"url.Error with the token", redactTestToken, fmt.Errorf("ntfy: post: %w", &url.Error{
			Op: "Post", URL: "https://push.example/" + redactTestToken, Err: context.DeadlineExceeded,
		})},
		{"url.Error to a fan-out node", endpointToken, fmt.Errorf("ntfy: fan-out to all 3 push nodes failed: %w", &url.Error{
			Op: "Post", URL: "http://10.0.0.5/" + redactTestTopic, Err: context.DeadlineExceeded,
		})},
		{"PushError message", redactTestToken, &PushError{Message: "apns: failed for /3/device/" + redactTestToken}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			core, logs := observer.New(zap.DebugLevel)
			d := New(nil, zap.New(core))
			d.Register(&fakeProvider{name: "apns", err: c.err})

			res := d.sendToDevicesDetailed(context.Background(),
				[]PushDevice{{Provider: "apns", Token: c.token}}, PushMessage{Title: "hi"})
			r := res.Results[0]
			if r.Success || r.Reason == "" {
				t.Fatalf("result = %+v, want a failure with a reason", r)
			}
			texts := []string{r.Reason, r.Message}
			for _, entry := range logs.All() {
				texts = append(texts, fmt.Sprint(entry.ContextMap()))
			}
			for _, text := range texts {
				for _, secret := range []string{redactTestToken, redactTestTopic} {
					if strings.Contains(text, secret) {
						t.Errorf("reported text carries %q: %q", secret, text)
					}
				}
			}
		})
	}
}

// An empty token must not turn every gap in the text into a marker.
func TestRedactFailureText_emptyTokenLeavesTextAlone(t *testing.T) {
	if got := redactFailureText("ntfy: no base URL", errors.New("x"), ""); got != "ntfy: no base URL" {
		t.Errorf("redactFailureText with no token = %q", got)
	}
}

func TestRedactRequestURL_keepsTheCauseAndDropsTheURL(t *testing.T) {
	err := RedactRequestURL(&url.Error{Op: "Post", URL: "https://h/" + redactTestToken, Err: context.DeadlineExceeded})
	if strings.Contains(err.Error(), redactTestToken) {
		t.Errorf("URL survived: %v", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("cause lost: %v", err)
	}
	plain := errors.New("not a transport error")
	if RedactRequestURL(plain) != plain {
		t.Error("a non-transport error was rewritten")
	}
}
