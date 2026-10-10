package httputil

import (
	"errors"
	"net/url"
)

// FailureReason is err as it may be logged or recorded. The HTTP client's
// error quotes the whole request URL, query string included, and a credential
// may be in it (an `api_key` or `token` parameter); only the cause is kept.
func FailureReason(err error) string {
	return WithoutURL(err).Error()
}

// WithoutURL is err, as the HTTP client returned it, without the request URL
// it quotes, and still matching errors.Is and errors.As for the cause. Wrap the
// result, not the other way round: an error that already carries context is
// returned as the cause of its client error, so the context would be lost.
func WithoutURL(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return urlErr.Err
	}
	return err
}

// WithoutQuery is rawURL with its user info, query and fragment dropped, for a
// log line that names the endpoint a request went to. A string that does not
// parse as a URL is not repeated at all.
func WithoutQuery(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "(unparseable URL)"
	}
	u.User = nil
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	u.RawFragment = ""
	return u.String()
}
