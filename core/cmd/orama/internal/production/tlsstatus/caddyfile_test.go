package tlsstatus

import (
	"slices"
	"testing"
)

// The Caddyfile the installer writes for a nameserver behind the SNI router,
// with ntfy (pkg/install/installers generateCaddyfile).
const generatedCaddyfile = `{
    email admin@example.org
    admin unix//run/orama-caddy/admin.sock|0600
    acme_ca https://acme-v02.api.letsencrypt.org/directory
    storage orama {
        endpoint http://localhost:6001/v1/internal/tls-store
        key_file /etc/caddy/orama-tls-store.key
    }
    https_port 8443
    servers {
        protocols h1
    }
}

*.example.org {
    tls {
        issuer acme {
            dns orama {
                endpoint http://localhost:6001/v1/internal/acme
                key_file /etc/caddy/orama-acme.key
            }
        }
    }
    reverse_proxy localhost:6001 {
        header_up Host {host}
    }
}

example.org {
    tls {
        issuer acme {
        }
    }
    reverse_proxy localhost:6001
}

http://*.example.org {
    reverse_proxy localhost:6001
}

http://example.org {
    reverse_proxy localhost:6001
}

push.example.org {
    tls {
    }
    reverse_proxy localhost:2586
}

:80 {
    reverse_proxy localhost:6001
}
`

func TestParseCaddyfile_generated(t *testing.T) {
	cf := parseCaddyfile(generatedCaddyfile)
	if cf.ACMECA != "https://acme-v02.api.letsencrypt.org/directory" {
		t.Errorf("acme_ca %q", cf.ACMECA)
	}
	if cf.HTTPSPort != 8443 {
		t.Errorf("https_port %d", cf.HTTPSPort)
	}
	if want := []string{"*.example.org", "example.org", "push.example.org"}; !slices.Equal(cf.Names, want) {
		t.Errorf("names %v, want %v: the http:// sites and :80 have no certificate", cf.Names, want)
	}
}

func TestParseCaddyfile_defaultsAndEmpty(t *testing.T) {
	cf := parseCaddyfile("")
	if cf.HTTPSPort != 443 || cf.ACMECA != "" || len(cf.Names) != 0 {
		t.Errorf("an empty file: %+v", cf)
	}
	cf = parseCaddyfile("{\n    email a@b.c\n}\n\nexample.org {\n    respond ok\n}\n")
	if cf.HTTPSPort != 443 || !slices.Equal(cf.Names, []string{"example.org"}) {
		t.Errorf("no https_port and no acme_ca: %+v", cf)
	}
}

func TestParseCaddyfile_commentsSnippetsAndLists(t *testing.T) {
	cf := parseCaddyfile("# a comment {\n(common) {\n    encode gzip\n}\na.example.org, https://b.example.org {\n    import common\n}\na.example.org {\n}\n")
	if want := []string{"a.example.org", "b.example.org"}; !slices.Equal(cf.Names, want) {
		t.Errorf("names %v, want %v", cf.Names, want)
	}
}

func TestParseCaddyfile_aBadPortKeepsTheDefault(t *testing.T) {
	if cf := parseCaddyfile("{\n    https_port abc\n}\n"); cf.HTTPSPort != 443 {
		t.Errorf("port %d", cf.HTTPSPort)
	}
}
