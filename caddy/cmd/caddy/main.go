// Command caddy is the Caddy the node runs: Caddy's standard modules plus the
// Orama DNS provider and certificate storage in the parent package.
//
// `orama build` compiles this package against this module's checked-in go.sum.
// It used to run xcaddy, which resolved its own temporary module on every
// build with the checksum database switched off, so two builds of the same
// release could hold different dependencies.
package main

import (
	caddycmd "github.com/caddyserver/caddy/v2/cmd"

	// Caddy's standard modules, as xcaddy's main.go imports them.
	_ "github.com/caddyserver/caddy/v2/modules/standard"

	// The Orama modules: dns.providers.orama and caddy.storage.orama.
	_ "github.com/DeBrosOfficial/caddy-orama"
)

func main() {
	caddycmd.Main()
}
