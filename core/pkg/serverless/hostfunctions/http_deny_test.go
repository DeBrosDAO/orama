package hostfunctions

import "testing"

func TestDenyInternalURL(t *testing.T) {
	denied := []string{
		"http://127.0.0.1/",
		"http://127.0.0.1:10100/db/query",
		"http://localhost/foo",
		"http://foo.localhost/",
		"http://[::1]/",
		"http://0.0.0.0/",
		"http://10.0.0.1/",
		"http://192.168.1.1/",
		"http://172.16.0.1/",
		"http://169.254.169.254/",
		"http://224.0.0.1/",
		"http://metadata.google.internal/",
		"file:///etc/passwd",
		"ftp://example.com/",
		"not-a-url",
		"",
	}
	for _, u := range denied {
		if err := denyInternalURL(u); err == nil {
			t.Errorf("denyInternalURL(%q) = nil, want error", u)
		}
	}
	allowed := []string{
		"https://example.com/x",
		"http://8.8.8.8/dns-query",
		"https://1.1.1.1/",
	}
	for _, u := range allowed {
		if err := denyInternalURL(u); err != nil {
			t.Errorf("denyInternalURL(%q) = %v, want nil", u, err)
		}
	}
}
