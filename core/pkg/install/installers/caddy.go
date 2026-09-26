package installers

import (
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	nodeauth "github.com/DeBrosOfficial/network/pkg/auth"
	"github.com/DeBrosOfficial/network/pkg/constants"
)

// internalAuthHeaders are the request headers a gateway uses to tell another
// gateway that it already authenticated the caller. They are meaningful only
// between gateways, so nothing arriving from the internet may carry one.
//
// The gateway itself refuses any that are not accompanied by a MAC keyed on the
// cluster secret. Caddy drops them a hop earlier because it is where the
// internet ends: it terminates TLS and reverse-proxies to localhost, so every
// public request reaches the gateway from 127.0.0.1 and forged headers travel
// with it by default. Two independent places have to fail before a forged
// header is believed.
var internalAuthHeaders = []string{
	"X-Internal-Auth-Validated",
	"X-Internal-Auth-Namespace",
	"X-Internal-Auth-JWT-Sub",
	"X-Internal-Auth-JWT-Custom",
	"X-Internal-Auth-Scopes",
	"X-Internal-Auth-MAC",
}

// proxyBlock renders a reverse_proxy directive that strips the internal-auth
// headers on the way up. The indentation matches the surrounding site block.
func proxyBlock(upstream string) string {
	var sb strings.Builder
	sb.WriteString("    reverse_proxy " + upstream + " {\n")
	for _, h := range internalAuthHeaders {
		sb.WriteString("        header_up -" + h + "\n")
	}
	sb.WriteString("    }")
	return sb.String()
}

// CaddyInstaller handles Caddy installation with custom DNS module
type CaddyInstaller struct {
	*BaseInstaller
	version   string
	oramaHome string

	// withNtfy, when set, causes generateCaddyfile to emit a reverse-
	// proxy block for `push.<dnsZone>` → localhost:<NtfyListenPort>.
	// Enabled per-node via EnableNtfyProxy. Feature #72.
	withNtfy     bool
	ntfyHostname string // e.g. "push.example.com" — fully-qualified public host

	// behindSNIRouter, when set, moves Caddy's HTTPS listener off :443 to
	// CaddyHTTPSPortBehindSNI so the orama-sni-router can own :443 and forward
	// TLS by SNI (feat-124, stealth TURN). Enabled per-node via
	// EnableSNIRouterMode. Plain HTTP (:80) is unaffected. When false the
	// generated Caddyfile is byte-identical to the pre-feature output.
	behindSNIRouter bool
}

// CaddyHTTPSPortBehindSNI is the port Caddy binds for HTTPS when the node runs
// behind the SNI router (which owns :443). 8443 matches the sni-router config's
// caddy fallback backend (127.0.0.1:8443) and the plan doc.
const CaddyHTTPSPortBehindSNI = 8443

// NewCaddyInstaller creates a new Caddy installer
func NewCaddyInstaller(arch string, logWriter io.Writer, oramaHome string) *CaddyInstaller {
	return &CaddyInstaller{
		BaseInstaller: NewBaseInstaller(arch, logWriter),
		version:       constants.CaddyVersion,
		oramaHome:     oramaHome,
	}
}

// EnableNtfyProxy tells the Caddy installer to emit a reverse-proxy
// block for the self-hosted ntfy server (feature #72). hostname is the
// public fully-qualified domain — e.g. "push.example.com" — that Caddy
// will obtain a Let's Encrypt cert for and route to the local ntfy
// server on NtfyListenPort.
//
// Must be called BEFORE Configure so the generated Caddyfile includes
// the block.
func (ci *CaddyInstaller) EnableNtfyProxy(hostname string) {
	ci.withNtfy = true
	ci.ntfyHostname = hostname
}

// EnableSNIRouterMode tells the Caddy installer to bind HTTPS on
// CaddyHTTPSPortBehindSNI (8443) instead of :443, freeing :443 for the
// orama-sni-router (feat-124). Plain HTTP on :80 is left untouched. Must be
// called BEFORE Configure so the generated Caddyfile picks up the global
// `https_port` option. A no-op when never called: the default Caddyfile keeps
// HTTPS on :443.
func (ci *CaddyInstaller) EnableSNIRouterMode() {
	ci.behindSNIRouter = true
}

// Configure creates Caddy configuration files.
// baseDomain is optional — if provided (and different from domain), Caddy will also
// serve traffic for the base domain and its wildcard (e.g., *.example.com).
// clusterSecret is what the key Caddy signs its DNS-01 calls with is derived
// from (CaddyACMEKeyPath).
func (ci *CaddyInstaller) Configure(domain string, email string, acmeEndpoint string, baseDomain string, acmeCA string, clusterSecret string) error {
	configDir := "/etc/caddy"
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// The key first: a Caddyfile naming a key file that is not there stops
	// Caddy from loading its config at all.
	if err := writeCaddyACMEKey(clusterSecret); err != nil {
		return err
	}

	// Create Caddyfile
	caddyfile := ci.generateCaddyfile(domain, email, acmeEndpoint, baseDomain, acmeCA)
	if err := os.WriteFile(filepath.Join(configDir, "Caddyfile"), []byte(caddyfile), 0644); err != nil {
		return fmt.Errorf("failed to write Caddyfile: %w", err)
	}

	return nil
}

// CaddyACMEKeyPath holds the key Caddy's orama DNS provider signs its
// /v1/internal/acme calls with, hex-encoded. root:orama 0640 in root-owned
// /etc/caddy: Caddy runs as orama and reads it; a tenant's deployment, which
// runs under a user of its own, cannot. The gateway derives the same key from
// the cluster secret (auth.ACMEChallengeKey).
const CaddyACMEKeyPath = "/etc/caddy/orama-acme.key"

// CaddyAdminSocket is Caddy's admin API. It used to be the default,
// localhost:2019: unauthenticated control of the process that terminates TLS
// for the node — load a config that proxies anything anywhere, or read the
// private keys — for every process on the host. A unix socket in Caddy's own
// runtime directory (RuntimeDirectory=orama-caddy, 0700), itself 0600, is
// reachable by the orama user only. `caddy reload` (the unit's ExecReload)
// reads the address from the Caddyfile.
const CaddyAdminSocket = "/run/orama-caddy/admin.sock"

// caddyAdminSocketMode is the admin socket's file mode, in the form Caddy's
// `unix/<path>|<mode>` listener address takes.
const caddyAdminSocketMode = "0600"

// writeCaddyACMEKey writes the key derived from clusterSecret to
// CaddyACMEKeyPath, root:orama 0640. It is created 0600 and only then handed
// to the group, so it is never readable by anyone else.
func writeCaddyACMEKey(clusterSecret string) error {
	key, err := nodeauth.ACMEChallengeKey(clusterSecret)
	if err != nil {
		return fmt.Errorf("derive the key Caddy signs DNS-01 requests with: %w", err)
	}
	if err := os.WriteFile(CaddyACMEKeyPath, []byte(hex.EncodeToString(key)+"\n"), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", CaddyACMEKeyPath, err)
	}
	return restrictToOramaGroup(CaddyACMEKeyPath)
}

// generateCaddyfile creates the Caddyfile configuration.
// If baseDomain is provided and different from domain, Caddy also serves
// the base domain and its wildcard (e.g., *.example.com alongside *.node1.example.com).
// acmeCA, when set, is the ACME directory every issuer uses: it is emitted as
// the global acme_ca option, which Caddy applies to each `issuer acme` block
// that names no directory of its own — including the TURN blocks appended at
// runtime. Empty keeps the default (Let's Encrypt production) and a
// byte-identical Caddyfile.
func (ci *CaddyInstaller) generateCaddyfile(domain, email, acmeEndpoint, baseDomain, acmeCA string) string {
	// Let's Encrypt via ACME DNS-01 challenge (no fallback to self-signed)
	tlsBlock := fmt.Sprintf(`    tls {
        issuer acme {
            dns orama {
                endpoint %s
                key_file %s
            }
        }
    }`, acmeEndpoint, CaddyACMEKeyPath)

	var sb strings.Builder
	// Caddy protocol restrictions:
	//   - HTTP/3 (QUIC) is disabled so Caddy doesn't bind UDP 443, which
	//     TURN needs for relay.
	//   - HTTP/2 is also disabled (bug #249). HTTP/2 forbids the
	//     `Connection: Upgrade` and `Upgrade: websocket` headers per
	//     RFC 7540 §8.1.2.2, so any WebSocket-upgrade request the
	//     client sends over an h2 connection arrives at Caddy with
	//     those headers stripped. Caddy then forwards a plain
	//     HTTP/1.1 GET to the backend gateway, which no longer
	//     recognises the request as a WS upgrade — its
	//     `isWebSocketUpgrade(r)` check fails and the
	//     query-string `?api_key=` / `?jwt=` WS-auth fallback is
	//     ignored, producing 401. RFC 8441 ("Bootstrapping WebSockets
	//     with HTTP/2") would fix this, but iOS RN and many other
	//     mobile WS libraries don't implement it. Until they do, h1
	//     is the only protocol that keeps WS auth working.
	//   - Cost: lose h2 multiplexing on regular HTTP traffic.
	//     Acceptable trade-off for an API gateway whose dominant
	//     workload is REST + WebSocket (neither benefits much from
	//     h2 stream multiplexing — REST is keep-alive over h1, and
	//     WS is single-connection by design).
	// When this node runs behind the SNI router (feat-124), move Caddy's HTTPS
	// listener off :443 to CaddyHTTPSPortBehindSNI via the `https_port` global
	// option. The sni-router owns :443 and forwards TLS by SNI to either a
	// namespace's TURNS listener or here (127.0.0.1:8443). Plain HTTP (:80) is
	// unchanged. When behindSNIRouter is false, no `https_port` line is emitted
	// and the Caddyfile is byte-identical to the pre-feature output.
	httpsPortOption := ""
	if ci.behindSNIRouter {
		httpsPortOption = fmt.Sprintf("    https_port %d\n", CaddyHTTPSPortBehindSNI)
	}
	acmeCAOption := ""
	if acmeCA != "" {
		acmeCAOption = fmt.Sprintf("    acme_ca %s\n", acmeCA)
	}
	adminOption := fmt.Sprintf("    admin unix/%s|%s\n", CaddyAdminSocket, caddyAdminSocketMode)
	sb.WriteString(fmt.Sprintf("{\n    email %s\n%s%s%s    servers {\n        protocols h1\n    }\n}\n", email, adminOption, acmeCAOption, httpsPortOption))

	gw := fmt.Sprintf("localhost:%d", constants.GatewayAPIPort)

	// Node domain blocks (e.g., node1.example.com, *.node1.example.com)
	sb.WriteString(fmt.Sprintf("\n*.%s {\n%s\n%s\n}\n", domain, tlsBlock, proxyBlock(gw)))
	sb.WriteString(fmt.Sprintf("\n%s {\n%s\n%s\n}\n", domain, tlsBlock, proxyBlock(gw)))

	// Base domain blocks (e.g., example.com, *.example.com) — for app routing
	if baseDomain != "" && baseDomain != domain {
		sb.WriteString(fmt.Sprintf("\n*.%s {\n%s\n%s\n}\n", baseDomain, tlsBlock, proxyBlock(gw)))
		sb.WriteString(fmt.Sprintf("\n%s {\n%s\n%s\n}\n", baseDomain, tlsBlock, proxyBlock(gw)))
	}

	// HTTP blocks — serve traffic over plain HTTP so the gateway is reachable
	// even when TLS certificates are unavailable (e.g., Let's Encrypt rate limits).
	// Without these, Caddy auto-redirects HTTP→HTTPS for the named domain blocks above.
	sb.WriteString(fmt.Sprintf("\nhttp://*.%s {\n%s\n}\n", domain, proxyBlock(gw)))
	sb.WriteString(fmt.Sprintf("\nhttp://%s {\n%s\n}\n", domain, proxyBlock(gw)))
	if baseDomain != "" && baseDomain != domain {
		sb.WriteString(fmt.Sprintf("\nhttp://*.%s {\n%s\n}\n", baseDomain, proxyBlock(gw)))
		sb.WriteString(fmt.Sprintf("\nhttp://%s {\n%s\n}\n", baseDomain, proxyBlock(gw)))
	}

	// Self-hosted ntfy reverse-proxy (feature #72). Emitted only when
	// the orchestrator has called EnableNtfyProxy on this installer —
	// i.e. this node was selected to host ntfy. The hostname is its
	// own block so the cert lives separately from the namespace gateway
	// cert (different rotation cadence, different blast radius).
	if ci.withNtfy && ci.ntfyHostname != "" {
		sb.WriteString(fmt.Sprintf("\n%s {\n%s\n%s\n}\n",
			ci.ntfyHostname, tlsBlock, proxyBlock(fmt.Sprintf("localhost:%d", NtfyListenPort))))
	}

	// HTTP catch-all fallback (handles remaining plain HTTP traffic)
	sb.WriteString(fmt.Sprintf("\n:80 {\n%s\n}\n", proxyBlock(gw)))

	return sb.String()
}
