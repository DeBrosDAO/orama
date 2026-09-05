package config

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/environments/templates"
)

// Every shape of the rendered node.yaml must survive the node's own strict
// decoder. The conditional blocks in the template are whitespace-sensitive —
// one stray indent turns the next key into a nested mapping and the node
// refuses to start — so each combination is rendered and parsed.
func TestRenderedNodeConfig_decodesStrictly_allShapes(t *testing.T) {
	base := templates.NodeConfigData{
		NodeID:         "node2",
		P2PPort:        4002,
		DataDir:        "/opt/orama/.orama/node2",
		RQLiteHTTPPort: 5002,
		RQLiteRaftPort: 7002,
	}

	withCreds := base
	withCreds.RQLiteUsername = "orama"
	withCreds.RQLitePassword = "deadbeef"
	withCreds.RQLiteAuthFile = "/home/orama/.orama/secrets/rqlite-auth.json"

	credsNoFile := base
	credsNoFile.RQLiteUsername = "orama"
	credsNoFile.RQLitePassword = "deadbeef"

	withCerts := withCreds
	withCerts.NodeCert = "/etc/orama/node.crt"
	withCerts.NodeKey = "/etc/orama/node.key"

	for name, data := range map[string]templates.NodeConfigData{
		"no credentials":       base,
		"credentials and file": withCreds,
		"credentials only":     credsNoFile,
		"credentials + certs":  withCerts,
	} {
		t.Run(name, func(t *testing.T) {
			out, err := templates.RenderNodeConfig(data)
			if err != nil {
				t.Fatal(err)
			}
			var cfg Config
			if err := DecodeStrict(strings.NewReader(out), &cfg); err != nil {
				t.Fatalf("strict decode: %v\n%s", err, out)
			}
			if cfg.Database.RQLiteUsername != data.RQLiteUsername {
				t.Fatalf("username %q, want %q", cfg.Database.RQLiteUsername, data.RQLiteUsername)
			}
			if cfg.Database.RQLiteAuthFile != data.RQLiteAuthFile {
				t.Fatalf("auth file %q, want %q", cfg.Database.RQLiteAuthFile, data.RQLiteAuthFile)
			}
			if cfg.Database.RQLiteEnforceAuth {
				t.Fatal("rendered config enables enforcement")
			}
		})
	}
}

func TestRenderedNodeConfig_carriesCredentials(t *testing.T) {
	out, err := templates.RenderNodeConfig(templates.NodeConfigData{
		NodeID:         "node2",
		P2PPort:        4002,
		DataDir:        "/opt/orama/.orama/node2",
		RQLiteHTTPPort: 5002,
		RQLiteRaftPort: 7002,
		RQLiteUsername: "orama",
		RQLitePassword: "deadbeef",
		RQLiteAuthFile: "/home/orama/.orama/secrets/rqlite-auth.json",
	})
	if err != nil {
		t.Fatal(err)
	}

	var cfg Config
	if err := DecodeStrict(strings.NewReader(out), &cfg); err != nil {
		t.Fatalf("strict decode of the rendered node config: %v\n%s", err, out)
	}
	if cfg.Database.RQLiteUsername != "orama" || cfg.Database.RQLitePassword != "deadbeef" {
		t.Fatalf("credentials did not survive the round trip: %q/%q",
			cfg.Database.RQLiteUsername, cfg.Database.RQLitePassword)
	}
	if cfg.Database.RQLiteAuthFile != "/home/orama/.orama/secrets/rqlite-auth.json" {
		t.Fatalf("auth file did not survive: %q", cfg.Database.RQLiteAuthFile)
	}
	if cfg.Database.RQLiteEnforceAuth {
		t.Fatal("rendered config enables enforcement")
	}
}
