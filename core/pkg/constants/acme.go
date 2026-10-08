package constants

// ACME directories a cluster's Caddy issues from (tls.acme_ca in node.yaml).
const (
	// LetsEncryptProductionACME is the default: certificates every client
	// trusts. The Caddyfile names it explicitly rather than leaving the choice
	// to Caddy's own default.
	LetsEncryptProductionACME = "https://acme-v02.api.letsencrypt.org/directory"
	// LetsEncryptStagingACME issues certificates no client trusts, under far
	// looser rate limits: for clusters rebuilt many times a week (the sandbox).
	LetsEncryptStagingACME = "https://acme-staging-v02.api.letsencrypt.org/directory"
)

// ACMECAAliases name the ACME directories an operator should not have to
// paste into --acme-ca.
var ACMECAAliases = map[string]string{
	"letsencrypt":         LetsEncryptProductionACME,
	"letsencrypt-staging": LetsEncryptStagingACME,
}
