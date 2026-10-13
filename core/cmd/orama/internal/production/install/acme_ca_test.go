package install

import "testing"

func TestResolveACMECA_AliasAndURLs(t *testing.T) {
	f := &Flags{ACMECA: "letsencrypt-staging"}
	if err := f.resolveACMECA(); err != nil || f.ACMECA != "https://acme-staging-v02.api.letsencrypt.org/directory" {
		t.Fatalf("alias: %q, %v", f.ACMECA, err)
	}
	f = &Flags{ACMECA: "https://ca.example/acme/directory"}
	if err := f.resolveACMECA(); err != nil || f.ACMECA != "https://ca.example/acme/directory" {
		t.Fatalf("url: %q, %v", f.ACMECA, err)
	}
	f = &Flags{}
	if err := f.resolveACMECA(); err != nil || f.ACMECA != "" {
		t.Fatalf("empty must stay empty (production): %q, %v", f.ACMECA, err)
	}
}

// The value lands in the Caddyfile's global block; anything that is not a
// plain https URL could rewrite it.
func TestResolveACMECA_RefusesNonHTTPSAndCaddyfileSyntax(t *testing.T) {
	for _, bad := range []string{"http://ca.example/dir", "staging", "https://", "https://ca.example/dir }\n:80 {", "https://ca.example/dir extra"} {
		f := &Flags{ACMECA: bad}
		if err := f.resolveACMECA(); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}
