package push

import (
	"errors"
	"net/url"
	"strings"
)

// A provider's transport error is a *url.Error, and its text is the request
// URL — which carries the device token (APNs /3/device/<token>) or the ntfy
// topic, possibly rewritten from what the device registered (a UnifiedPush
// endpoint reduced to its path, posted to another node's address). Per-device
// results go back to the calling function, and for a push topic the caller may
// be relaying them to a sender who must never learn that token. So providers
// drop the URL where the error is made (httputil.WithoutURL), and the dispatcher scrubs what it
// reports as well.

const (
	// redactedDeviceToken replaces a device token wherever error text repeats it.
	redactedDeviceToken = "[device-token]"

	// redactedRequestURL replaces a request URL left in error text.
	redactedRequestURL = "[request-url]"
)

// redactFailureText is err's text with any request URL and the device token
// removed, for the per-device result and the dispatcher's log.
func redactFailureText(text string, err error, token string) string {
	var ue *url.Error
	if errors.As(err, &ue) && ue.URL != "" {
		text = strings.ReplaceAll(text, ue.URL, redactedRequestURL)
	}
	if token != "" {
		text = strings.ReplaceAll(text, token, redactedDeviceToken)
	}
	return text
}
