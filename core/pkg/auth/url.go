package auth

import (
	"net/url"
	"strings"
)

// extractDomainFromURL extracts the hostname from a URL, stripping scheme, port, and path.
func extractDomainFromURL(rawURL string) string {
	// Ensure the URL has a scheme so net/url.Parse works correctly
	if !strings.Contains(rawURL, "://") {
		rawURL = "https://" + rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}
